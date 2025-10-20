package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	adsCache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	adsServer "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
	"sync"
	"time"
)

// ===================
// Manager Constructor
// ===================

// NewManager creates a new Manager instance
// It will create the database, nats and ads manager instances
func NewManager() (*Manager, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	// Create the context and ctxCancel the function.
	ctx, ctxCancel := context.WithCancel(context.Background())

	databaseManager, err := newDBManager(ctx, config)
	if err != nil {
		ctxCancel() // to avoid leaking resources
		return nil, fmt.Errorf("failed to init database manager: %w", err)
	}

	natsManager, err := newNATSManager(ctx, config)
	if err != nil {
		ctxCancel() // to avoid leaking resources
		return nil, fmt.Errorf("failed to init nats manager: %w", err)
	}

	adsManager, err := newADSManager(ctx, config)
	if err != nil {
		ctxCancel() // to avoid leaking resources
		return nil, fmt.Errorf("failed to init ads manager: %w", err)
	}

	return &Manager{
		wg:        &sync.WaitGroup{},
		ctx:       ctx,
		ctxCancel: ctxCancel,

		Config: config,
		DB:     databaseManager,
		NATS:   natsManager,
		ADS:    adsManager,
	}, nil
}

// newDBManager creates a new DatabaseManager instance
// It will create the database instances and migrate the tables
func newDBManager(ctx context.Context, config *Config) (*DatabaseManager, error) {
	// Create the database instances
	readOnlyDB, readOnlyDBClose, err := openSQLite(config.DatabaseFilePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	readWriteDB, readWriteDBClose, err := openSQLite(config.DatabaseFilePath, true)
	if err != nil {
		readWriteDBClose()
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	// Migrate the tables
	err = MigrateTables(readWriteDB)
	if err != nil {
		readWriteDBClose()
		readOnlyDBClose()
		return nil, fmt.Errorf("failed to init manager: %w", err)
	}

	// Database Manager
	return &DatabaseManager{
		ctx:       ctx,
		ReadOnly:  readOnlyDB,
		ReadWrite: readWriteDB,
	}, nil
}

// newNATSManager creates a new NATSManager instance
func newNATSManager(ctx context.Context, config *Config) (*NATSManager, error) {
	return &NATSManager{
		ctx:                  ctx,
		Config:               &config.NatsConfig,
		IncomingStream:       "proxy." + config.AgentID + ".request.>",
		IncomingStreamPrefix: "proxy." + config.AgentID + ".request.",
		OutgoingStream:       "proxy." + config.AgentID + ".reply.>",
		OutgoingStreamPrefix: "proxy." + config.AgentID + ".reply.",
		MessageChan:          make(chan *nats.Msg, 1000),
	}, nil
}

// newADSManager creates a new ADSManager instance
func newADSManager(ctx context.Context, config *Config) (*ADSManager, error) {
	adsSnapshotCache := adsCache.NewSnapshotCache(true, adsCache.IDHash{}, nil)
	adsManager := ADSManager{
		ctx:                   ctx,
		Server:                nil,
		Config:                &config.ADSConfig,
		InitializedNodes:      map[string]bool{},
		InitializedNodesMutex: sync.RWMutex{},
		LatestSnapshotVersion: "",
		LatestSnapshot:        nil,
		CacheStore:            &adsSnapshotCache,
		CacheOperationMutex:   sync.Mutex{},
	}
	adsServerInstance := adsServer.NewServer(ctx, adsSnapshotCache, &adsManager)
	adsManager.Server = &adsServerInstance
	return &adsManager, nil
}

// ===============
// Manager Methods
// ===============

// Start method will start different goroutines to perform different tasks
func (m *Manager) Start() {
	go m.listenToStream()
	go m.consumeMessages()
	go m.processRequests()
	go m.publishResponses()
	go m.ADS.RunServer(m.wg)
}

// GracefulStop stops the associated goroutines gracefully
// It will wait for all the goroutines to finish
func (m *Manager) GracefulStop() {
	fmt.Println("Started graceful shutdown")
	m.ctxCancel()
	m.wg.Wait()
	fmt.Println("Graceful shutdown completed")
}

// listenToStream listens to the incoming nats jet stream (proxy.<node_id>.request.>) and pushes the messages to NATS.MessageChan
// from which the consumerMessages goroutine processes the messages
func (m *Manager) listenToStream() {
	m.wg.Add(1)
	defer m.wg.Done()

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
	// Because it's very important for Regional ADS to be online
	// So that it can serve proxies required configs
	// If NATS Server become unavailable, at max that should pause updates
	for {

		// check context deadline
		if err = m.ctx.Err(); err != nil {
			return
		}

		natsConn, err = m.createNATSConnection()
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
		fmt.Printf("Subscribing to NATS Stream: %s\n", m.NATS.IncomingStream)
		subscription, err = js.ChanSubscribe(
			m.NATS.IncomingStream,
			m.NATS.MessageChan,
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

	// Check if context is throwing error, probably it's already canceled
	if err = m.ctx.Err(); err != nil {
		return
	}

	//	Wait until context is canceled
	<-m.ctx.Done()
}

// consumeMessages consumes the messages from NATS.MessageChan and stores them in the DB
// Flow of workings -
//  1. Read all messages from the channel, so that we can batch those together
//  2. Parse the messages and store them in the database. Uses (m *Manager).storeMessage(...) method
//  3. In the above process, we will ignore requests with having duplicate request_id in payload
//  4. Acknowledge messages so that nats do not re-deliver them
//  5. Wait for 250 ms and repeat from step 1
func (m *Manager) consumeMessages() {
	m.wg.Add(1)
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			fmt.Print("ctx is cancelled\n")
			return

		default:
			// read all messages from the channel so that we can batch those together
			messages := ReadAllMessagesOfChannel(m.NATS.MessageChan)
			if len(messages) == 0 {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			for _, msg := range messages {
				m.storeMessage(msg)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// processRequests pull the new messages from the database and do the necessary action
// Internally this triggers MessageHandler.process(...) for each event to gather the response payload
// Flow of workings -
//  1. Fetch the top 250 pending events / messages from database
//  2. If there is no pending event, wait for 1 s and repeat from step 1
//  3. Process each message, which will
//     - call MessageHandler.process(...) method and
//     - that will only do changes in the database only
//  4. Clean up unused backends and listeners from database
//  5. Commit the transaction
//  6. Generate in-memory ads cache snapshot, envoy-go-control-plane will automatically broadcast the changes
//  7. Wait for 100 ms and repeat from step 1
func (m *Manager) processRequests() {
	m.wg.Add(1)
	defer m.wg.Done()

	var messages []Message

	for {
		select {
		case <-m.ctx.Done():
			fmt.Print("ctx is cancelled\n")
			return
		default:
			//	Fetch the top 250 messages from the DB
			tx := m.DB.ReadOnly.Where("processed = ?", false).Order("queued_at asc").Limit(250).Find(&messages)
			if tx.Error != nil {
				fmt.Printf("failed to fetch messages from DB: %v\n", tx.Error)
				time.Sleep(1 * time.Second)
				continue
			}

			// If no result continues
			if len(messages) == 0 {
				time.Sleep(1 * time.Second)
				continue
			}

			// Create a db transaction
			tx = m.DB.ReadWrite.Begin()

			//	Process each message
			for _, msg := range messages {
				ProcessMessage(tx, &msg)
			}

			// Cleanup unused backends and listeners
			err := pruneOrphanedResources(tx)
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

			m.ADS.generateAndBroadcastADSChanges(m.DB)

			//	Force GC
			messages = []Message{}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// publishResponses pulls the messages from the database and publishes the response payloads to proxy.<node_id>.reply.> subjects
// Flow of workings -
//  1. Fetch the messages from the database
//  2. If there is no message, wait for 1 s and repeat from step 1
//  3. Publish the response payloads to the reply subjects
//  4. Mark the messages as replied in database
//  5. Wait for 100 ms and repeat from step 1
func (m *Manager) publishResponses() {
	m.wg.Add(1)
	defer m.wg.Done()

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
		case <-m.ctx.Done():
			fmt.Print("ctx is cancelled\n")
			return
		default:
			if natsConn == nil {
				natsConn, err = m.createNATSConnection()
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

			tx := m.DB.ReadOnly.Where("processed = ? AND replied = ?", true, false).Find(&messages).Limit(200)
			if tx.Error != nil {
				fmt.Printf("Failed to find messages to send: %v\n", tx.Error)
				return
			}

			if len(messages) == 0 {
				continue
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

			// Publish the responses in reply subjects
			for _, payload := range responsePayloads {
				payloadJSONBytes, err = json.MarshalIndent(payload, "", "  ")
				if err != nil {
					fmt.Printf("Failed to marshal message: %v\n", err)
					continue
				}

				if _, err = js.Publish(fmt.Sprintf("%s%s", m.NATS.OutgoingStreamPrefix, payload.Event), payloadJSONBytes); err != nil {
					fmt.Printf("Failed to publish message: %v\n", err)
					continue
				}

				// Add to acked messages
				ackedMessages = append(ackedMessages, payload.MessageID)
			}

			// Mark messages as replied
			tx = m.DB.ReadWrite.Model(&Message{}).Where("id IN (?)", ackedMessages).Updates(Message{Replied: true})
			if tx.Error != nil {
				fmt.Printf("Failed to mark messages as replied: %v\n", tx.Error)
			}

			// Force GC
			ackedMessages = []uint{}
			messages = []Message{}
			responsePayloads = []ResponsePayloadV1{}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// ===============
// Helper Methods
// ===============

// createNATSConnection creates a new NATS connection to the configured NATS server
func (m *Manager) createNATSConnection() (*nats.Conn, error) {
	return nats.Connect(fmt.Sprintf("nats://%s:%d", m.Config.NatsConfig.Host, m.Config.NatsConfig.Port),
		nats.Name(m.Config.AgentID),
		nats.UserJWTAndSeed(m.NATS.Config.JWT, m.NATS.Config.NKey),
		nats.MaxReconnects(-1),
	)
}

// parseEventNameFromSubject extracts the event name from the NATS subject,
// Subject should be in the format of proxy.<node_id>.request.<event_name>
// This function will extract the event name from the subject
func (m *Manager) parseEventNameFromSubject(subject string) string {
	if len(subject) < len(m.NATS.IncomingStreamPrefix) {
		return ""
	}
	return subject[len(m.NATS.IncomingStreamPrefix):]
}

// isMessageExist checks if the message with a given event and request_id exists in the database
// If it exists, it returns true, else false
// If it faces some other issue, will return error; in that case, it can't be sure if the message exists or not
func (m *Manager) isMessageExist(event string, requestID string) (bool, error) {
	tx := m.DB.ReadOnly.First(&Message{}, "event = ? AND request_id = ?", event, requestID)
	if tx.Error != nil {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, tx.Error
	}
	return true, nil
}

// storeMessage validates and stores a message in the database, acknowledging it to NATS.
// If the message is re-delivered, duplicate detection ensures it's acknowledged safely.
func (m *Manager) storeMessage(msg *nats.Msg) {
	event := m.parseEventNameFromSubject(msg.Subject)
	if len(event) == 0 {
		_ = msg.Ack()
		return
	}

	//	Try to parse the message
	isParsed, requestID, requestedAt, request, err := ParseEvent(event, msg.Data)
	if !isParsed || err != nil {
		_ = msg.Ack()
		fmt.Println("Acknowledging message: ", msg.Subject, " ", msg.Data, " ", err)

		if err != nil {
			fmt.Printf("Failed to parse event: %v\n", err)
		}
		return
	}

	// Avoid duplicate messages
	isExist, err := m.isMessageExist(event, requestID)
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
	tx := m.DB.ReadWrite.Create(&msgEntry)
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
