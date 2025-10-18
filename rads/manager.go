package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
	"sync"
	"sync/atomic"
	"time"
)

type Manager struct {
	Config *Config

	IncomingStream       string
	IncomingStreamPrefix string
	OutgoingStream       string
	OutgoingStreamPrefix string
	NATSMessageChan      chan *nats.Msg

	HasPendingProxyChanges atomic.Bool

	ReadOnlyDB  *gorm.DB
	ReadWriteDB *gorm.DB

	Wg            *sync.WaitGroup
	Context       context.Context
	CancelContext context.CancelFunc
}

func NewManager() (*Manager, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	readOnlyDB, readOnlyDBClose, err := openSQLite(config.DatabaseFilePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	readWriteDB, readWriteDBClose, err := openSQLite(config.DatabaseFilePath, true)
	if err != nil {
		readWriteDBClose()
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	err = MigrateTables(readWriteDB)
	if err != nil {
		readWriteDBClose()
		readOnlyDBClose()
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	// Create the context and cancel the function.
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		Config: config,

		IncomingStream:       "proxy." + config.AgentID + ".request.>",
		IncomingStreamPrefix: "proxy." + config.AgentID + ".request.",

		OutgoingStream:       "proxy." + config.AgentID + ".reply.>",
		OutgoingStreamPrefix: "proxy." + config.AgentID + ".reply.",

		NATSMessageChan: make(chan *nats.Msg, 1000),

		HasPendingProxyChanges: atomic.Bool{},

		ReadOnlyDB:  readOnlyDB,
		ReadWriteDB: readWriteDB,

		Wg:            &sync.WaitGroup{},
		Context:       ctx,
		CancelContext: cancel,
	}, nil
}

func (m *Manager) ParseEventTypeFromSubject(subject string) string {
	if len(subject) < len(m.IncomingStreamPrefix) {
		return ""
	}
	return subject[len(m.IncomingStreamPrefix):]
}

func (m *Manager) CreateNATSConnection() (*nats.Conn, error) {
	return nats.Connect(fmt.Sprintf("nats://%s:%d", m.Config.NatsConfig.Host, m.Config.NatsConfig.Port), nats.Name(m.Config.AgentID), nats.MaxReconnects(-1))
}

func (m *Manager) ListenToStream() {
	m.Wg.Add(1)
	defer m.Wg.Done()

	var natsConn *nats.Conn
	var js nats.JetStreamContext
	var subscription *nats.Subscription
	var err error

	defer func() {
		if subscription != nil {
			err := subscription.Unsubscribe()
			if err != nil {
				fmt.Printf("Failed to unsubscribe from NATS Stream: %v\n", err)
			}
		}

		if natsConn != nil {
			natsConn.Close()
			natsConn = nil
		}
	}()

	// NATS Server could be down, so we need to wait until it comes up
	// Because, it's very important for Regional ADS to be online
	// So that, it can serve proxies required configs
	// If NATS Server become unavailable, at max that should pause updates
	for {

		// check context deadline
		if err = m.Context.Err(); err != nil {
			return
		}

		natsConn, err = m.CreateNATSConnection()
		if err != nil {
			fmt.Printf("Failed to connect to NATS Server: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Create Jet Stream context
		js, err = natsConn.JetStream(nats.PublishAsyncMaxPending(1000))
		if err != nil {
			natsConn.Close()
			natsConn = nil
			fmt.Printf("Failed to create Jet Stream context: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Subscribe to the stream
		fmt.Printf("Subscribing to NATS Stream: %s\n", m.IncomingStream)
		subscription, err = js.ChanSubscribe(
			m.IncomingStream,
			m.NATSMessageChan,
			nats.ManualAck(),
			nats.AckWait(1*time.Minute),
			nats.Durable(fmt.Sprintf("proxy-%s", m.Config.AgentID)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		)
		if err != nil {
			natsConn.Close()
			natsConn = nil
			fmt.Printf("Failed to subscribe to NATS Stream: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

	// Add some logging handler
	natsConn.SetReconnectHandler(func(conn *nats.Conn) {
		fmt.Printf("Reconnected to NATS Server\n")
	})

	// Check if context is throwing error, probably it's already cancelled
	if err = m.Context.Err(); err != nil {
		return
	}

	//	Wait until context is cancelled
	<-m.Context.Done()
}

func (m *Manager) StoreRequestsAndAcknowledge() {
	m.Wg.Add(1)
	defer m.Wg.Done()

	for {
		select {
		case <-m.Context.Done():
			fmt.Print("Context is cancelled\n")
			return

		default:
			// read all messages from the channel, so that we can batch those together
			messages := ReadAllMessagesOfChannel(m.NATSMessageChan)
			if len(messages) == 0 {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			for _, msg := range messages {
				m.StoreMessage(msg)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (m *Manager) IsMessageExist(event string, requestID string) (bool, error) {
	tx := m.ReadOnlyDB.First(&Message{}, "event = ? AND request_id = ?", event, requestID)
	if tx.Error != nil {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, tx.Error
	}
	return true, nil
}

func (m *Manager) StoreMessage(msg *nats.Msg) {
	event := m.ParseEventTypeFromSubject(msg.Subject)
	if len(event) == 0 {
		_ = msg.Ack()
		return
	}

	//	Try to parse the message
	isParsed, requestID, requestedAt, request, err := parseEvent(event, msg.Data)
	if !isParsed || err != nil {
		_ = msg.Ack()
		fmt.Println("Acknowledging message: ", msg.Subject, " ", msg.Data, " ", err)

		if err != nil {
			fmt.Printf("Failed to parse event: %v\n", err)
		}
		return
	}

	// Avoid duplicate messages
	isExist, err := m.IsMessageExist(event, requestID)
	if isExist {
		_ = msg.Ack()
		return
	}

	if err != nil {
		// NOTE: Don't ack the message, because it's not a duplicate
		_ = msg.Nak()
		fmt.Printf("Failed to check if message exists: %v\n", err)
		return
	}

	// Try to marshal the request
	requestPayload, err := json.Marshal(request)
	if err != nil {
		_ = msg.Ack() // If marshaling fails, no point in retrying
		fmt.Printf("Failed to marshal request: %v\n", err)
		return
	}

	currentTime := time.Now().UTC()

	msgEntry := Message{
		Event:           event,
		RequestID:       requestID,
		RequestPayload:  string(requestPayload),
		ResponsePayload: "{}",
		Processed:       false,
		Replied:         false,
		RequestedAt:     requestedAt,
		QueuedAt:        &currentTime,
		ProcessedAt:     nil,
	}

	//	Insert in DB
	tx := m.ReadWriteDB.Create(&msgEntry)
	if tx.Error != nil {
		_ = msg.Nak() // We want to retry this message
		fmt.Printf("Failed to insert message in DB: %v\n", err)
		return
	}
	err = msg.Ack()
	if err != nil {
		fmt.Printf("Failed to ack message: %v\n", err)
	}
}

func (m *Manager) ProcessRequests() {
	m.Wg.Add(1)
	defer m.Wg.Done()

	var messages []Message

	for {
		select {
		case <-m.Context.Done():
			fmt.Print("Context is cancelled\n")
			return
		default:
			//	Fetch top 100 messages from the DB
			tx := m.ReadOnlyDB.Where("processed = ?", false).Order("queued_at asc").Limit(100).Find(&messages)
			if tx.Error != nil {
				fmt.Printf("failed to fetch messages from DB: %v\n", tx.Error)
				time.Sleep(1 * time.Second)
				continue
			}

			// If no result continue
			if len(messages) == 0 {
				time.Sleep(1 * time.Second)
				continue
			}

			// Create an db transaction
			tx = m.ReadWriteDB.Begin()

			//	Process each message
			for _, msg := range messages {
				processMessage(tx, &msg)
			}

			// Cleanup unused backends and listeners
			err := cleanupUnusedBackendsAndListeners(tx)
			if err != nil {
				fmt.Printf("failed to cleanup unused records: %v\n", err)
			}

			// Commit the transaction
			err = tx.Commit().Error
			if err != nil {
				fmt.Printf("Failed to commit transaction: %v\n", err)
				time.Sleep(1 * time.Second)
				continue
			}

			m.BroadcastChangesToProxies()

			//	Force GC
			messages = []Message{}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func (m *Manager) SendResponsesToQueue() {
	m.Wg.Add(1)
	defer m.Wg.Done()

	// cleanup already sent ones TODO

	var natsConn *nats.Conn
	var js nats.JetStreamContext
	var err error
	// Find messages with status Processed but not replied
	var messages []Message
	var responsePayloads []ResponsePayloadV1
	var ackedMessages []uint
	var payloadJSONBytes []byte

	defer func() {
		if natsConn != nil {
			natsConn.Close()
			natsConn = nil
			js = nil
		}
	}()

	for {
		select {
		case <-m.Context.Done():
			fmt.Print("Context is cancelled\n")
			return
		default:
			if natsConn == nil {
				natsConn, err = m.CreateNATSConnection()
				if err != nil {
					fmt.Printf("Failed to connect to NATS Server: %v\n", err)
					natsConn.Close()
					natsConn = nil
					time.Sleep(1 * time.Second)
					continue
				}

				// Create Jet Stream context
				js, err = natsConn.JetStream(nats.PublishAsyncMaxPending(1000))
				if err != nil {
					natsConn.Close()
					natsConn = nil
					js = nil
					fmt.Printf("Failed to create Jet Stream context: %v\n", err)
					time.Sleep(1 * time.Second)
					continue
				}
			}

			tx := m.ReadOnlyDB.Where("processed = ? AND replied = ?", true, false).Find(&messages).Limit(200)
			if tx.Error != nil {
				fmt.Printf("Failed to find messages to send: %v\n", tx.Error)
				return
			}

			// Prepare the responses
			for _, msg := range messages {
				payload := ResponsePayloadV1{
					Event:        msg.Event,
					MessageID:    msg.ID,
					Success:      msg.Success,
					Data:         json.RawMessage(msg.ResponsePayload),
					ErrorMessage: msg.ErrorMessage,
					ProcessedAt:  *msg.ProcessedAt,
					QueuedAt:     *msg.QueuedAt,
				}
				payload.RequestID = msg.RequestID
				payload.RequestedAt = *msg.RequestedAt

				responsePayloads = append(responsePayloads, payload)
			}

			if len(messages) == 0 {
				continue
			}

			// Publish the responses in reply subjects
			for _, payload := range responsePayloads {
				payloadJSONBytes, err = json.MarshalIndent(payload, "", "  ")
				if err != nil {
					fmt.Printf("Failed to marshal message: %v\n", err)
					continue
				}

				if _, err = js.Publish(fmt.Sprintf("%s%s", m.OutgoingStreamPrefix, payload.Event), payloadJSONBytes); err != nil {
					fmt.Printf("Failed to publish message: %v\n", err)
					continue
				}

				// Add to acked messages
				ackedMessages = append(ackedMessages, payload.MessageID)
			}

			// Mark messages as replied
			tx = m.ReadWriteDB.Model(&messages).Where("id IN (?)", ackedMessages).Updates(Message{Replied: true})
			if tx.Error != nil {
				fmt.Printf("Failed to mark messages as replied: %v\n", tx.Error)
			}

			// Force GC
			ackedMessages = []uint{}
			messages = []Message{}
		}
	}
}

func (m *Manager) BroadcastChangesToProxies() {
	m.HasPendingProxyChanges.Store(true)
}

func (m *Manager) ListenForBroadcastChangesToProxies() {
	m.Wg.Add(1)
	defer m.Wg.Done()

	for {
		select {
		case <-m.Context.Done():
			fmt.Print("Context is cancelled\n")
			return
		default:
			if m.HasPendingProxyChanges.Swap(false) {
				continue
			}
		}
	}
}

func (m *Manager) Close() {
	return
}
