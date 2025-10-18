package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"reflect"
	"time"
)

// EventToRequestTypeMapping is a mapping between an event and the event/message type
// This will help to find the correct event type and process the message.
//
// The event type must implement the MessageHandler interface.
var EventToRequestTypeMapping = map[string]reflect.Type{
	"v1.tls_certificate.upsert":    reflect.TypeOf(TLSCertificateUpsertV1{}),
	"v1.tls_certificate.delete":    reflect.TypeOf(TLSCertificateDeleteV1{}),
	"v1.ingress_rule.upsert":       reflect.TypeOf(IngressRuleUpsertV1{}),
	"v1.ingress_rule.delete":       reflect.TypeOf(IngressRuleDeleteV1{}),
	"v1.http_redirect_rule.upsert": reflect.TypeOf(HTTPRedirectRuleUpsertV1{}),
	"v1.http_redirect_rule.delete": reflect.TypeOf(HTTPRedirectRuleDeleteV1{}),
}

// ================
// Message Handler
// ================

// MessageHandler is an interface to enforce the contract for all event handlers.
// All event handlers must implement this interface.
//
// The contract is simple:
//  1. Process() method is called when a new event is received.
//  2. Validate() method is called when a new event is received.
//     This method is used to validate the event before processing it.
//     If the event is invalid, the error should be returned.
//     If the event is valid, the error should be nil.
type MessageHandler interface {
	Process(db *gorm.DB) (reply json.RawMessage, err error)
}

// Compile time checks -- to prevent shipping functions with invalid signature or unimplemented method
// Because, later we are going to use reflection to call the function based on the event type.
// So, we will assume that the function signature is correct.
var (
	_ MessageHandler = (*TLSCertificateUpsertV1)(nil)
	_ MessageHandler = (*TLSCertificateDeleteV1)(nil)
	_ MessageHandler = (*IngressRuleUpsertV1)(nil)
	_ MessageHandler = (*IngressRuleDeleteV1)(nil)
	_ MessageHandler = (*HTTPRedirectRuleUpsertV1)(nil)
	_ MessageHandler = (*HTTPRedirectRuleDeleteV1)(nil)
)

// Please Note:
// In case of error, don't expect that transaction will be rolled back.
// It's up to the `Process` function to handle the error and roll back the changes.
// Any raised error will be propagated to the client.

// =======================================
// TLSCertificate Related Events' Handlers
// =======================================

func (r *TLSCertificateUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	if r.Domain == "" {
		return nil, errors.New("domain is required")
	}
	if r.Cert == "" || r.Key == "" {
		return nil, errors.New("cert and key are required")
	}

	tlsCertificateRecord, err := UpsertTLSCertificate(db, r.Domain, r.IsWildcard, r.Cert, r.Key)

	// Prepare Response
	jsonStr, err := json.Marshal(tlsCertificateRecord)
	if err != nil {
		return nil, err
	}

	return jsonStr, nil
}

func (r *TLSCertificateDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	id := GetTLSCertificateID(r.Domain, r.IsWildcard)

	isExist, err := recordExists(db, &TLSCertificate{}, id)
	if err != nil {
		return nil, err
	}
	if !isExist {
		return nil, nil
	}

	err = db.Delete(&TLSCertificate{ID: id}).Error
	return nil, err
}

// ======================================
// Ingress Rule Related Events' Handlers
// ======================================

func (r *IngressRuleUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	if (r.Protocol == HTTP || r.IsTLS) && r.Domain == "" {
		return nil, errors.New("domain is required for HTTP protocol or TCP TLS")
	}

	if r.Protocol == HTTP && r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	// Create / Update the listener
	listener, err := UpsertListener(db, r.BindIP, r.Port, r.Protocol, r.IsTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert listener: %w", err)
	}

	// Create / Update the backend
	backend, err := UpsertBackend(db, r.BackendResolver, r.BackendDNSResolver, r.BackendHosts, r.BackendPort, r.IsTLS, r.BackendSNIDomain)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert backend: %w", err)
	}

	// Create / Update the ingress rule
	ingressRule, err := UpsertIngressRule(db, r.Protocol, listener.ID, r.Domain, r.RoutePrefix, backend.ID, r.AllowedCIDRs, r.DeniedCIDRs, r.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert ingress rule: %w", err)
	}

	// Try to load the associations
	_ = db.Model(&ingressRule).Association("Listener").Find(&ingressRule.Listener)
	_ = db.Model(&ingressRule).Association("Backend").Find(&ingressRule.Backend)

	// Prepare Response
	jsonStr, err := json.Marshal(ingressRule)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ingress rule payload : %w", err)
	}
	return jsonStr, nil
}

func (r *IngressRuleDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	if r.Protocol == HTTP && r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	err := DeleteIngressRule(db, r.Protocol, GetListenerID(r.BindIP, r.Port), r.Domain, r.RoutePrefix)
	return nil, err
}

// ===========================================
// HTTP Redirect Rule Related Events' Handlers
// ===========================================

func (r *HTTPRedirectRuleUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	if r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	// Create / Update the listener
	listener, err := UpsertListener(db, r.BindIP, r.Port, HTTP, r.IsTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert listener: %w", err)
	}

	if r.StatusCode != 301 && r.StatusCode != 302 && r.StatusCode != 303 && r.StatusCode != 307 && r.StatusCode != 308 {
		return nil, errors.New("status_code is required and must be one of 301, 302, 307, 308")
	}

	// Ensure one redirect config has been provided
	if r.SchemeRedirect == "" && r.HostRedirect == "" && r.PathRedirect == "" {
		return nil, errors.New("one of scheme_redirect, host_redirect, path_redirect is required")
	}

	// Create / Update the redirect rule
	redirectRule, err := UpsertHTTPRedirectRule(db, listener.ID, r.Domain, r.RoutePrefix, r.SchemeRedirect, r.HostRedirect, r.PathRedirect, r.StatusCode, r.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert redirect rule: %w", err)
	}

	// Try to load the associations
	_ = db.Model(&redirectRule).Association("Listener").Find(&redirectRule.Listener)

	// Prepare Response
	jsonStr, err := json.Marshal(redirectRule)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal redirect rule payload : %w", err)
	}
	return jsonStr, nil
}

func (r *HTTPRedirectRuleDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	// Set the default route prefix to "/" if not provided
	if r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	return nil, DeleteHTTPRedirectRule(db, GetListenerID(r.BindIP, r.Port), r.Domain, r.RoutePrefix)
}

// ================
// Utility Functions
// ================

// ParseEvent parses the event received on reply subjects
// It also validates CommonEventParamsV1 fields and
// converts the message to it's correct event type.
func ParseEvent(event string, data []byte) (isParsed bool, requestID string, requestedAt *time.Time, message MessageHandler, err error) {
	if requestType, ok := EventToRequestTypeMapping[event]; ok {
		request := reflect.New(requestType).Interface()
		if err2 := json.Unmarshal(data, request); err2 != nil {
			isParsed = false
			err = fmt.Errorf("failed to unmarshal event (%s) data: %w ", event, err2)
			return
		}

		// Validate required fields
		v := reflect.ValueOf(request).Elem() // get the underlying struct

		// Check RequestID
		reqIDField := v.FieldByName("RequestID")
		if !reqIDField.IsValid() || reqIDField.Kind() != reflect.String || reqIDField.String() == "" {
			isParsed = false
			err = fmt.Errorf("missing or empty RequestID in event %s", event)
			return
		}

		// Check RequestedAt
		reqAtField := v.FieldByName("RequestedAt")
		if !reqAtField.IsValid() || reqAtField.Type() != reflect.TypeOf(time.Time{}) || reqAtField.Interface().(time.Time).IsZero() {
			isParsed = false
			err = fmt.Errorf("missing or zero RequestedAt in event %s", event)
			return
		}

		reqAtFieldTime := reqAtField.Interface().(time.Time)

		isParsed = true
		requestID = reqIDField.String()
		requestedAt = &reqAtFieldTime
		message = request.(MessageHandler)
	} else {
		isParsed = false
		err = errors.New("unknown event: " + event)
	}
	return
}

// ProcessMessage processes the message received on reply subjects
// It internally calls the specific event's Process() function.
// It also updates the message's success and error fields.
func ProcessMessage(db *gorm.DB, msg *Message) {
	currentTime := time.Now().UTC()
	msg.ProcessedAt = &currentTime
	msg.Success = false
	msg.Processed = true
	msg.ResponsePayload = "{}"
	msg.ErrorMessage = ""

	defer func() {
		err := db.Save(&msg).Error
		if err != nil {
			fmt.Printf("failed to save message: %v\n", err)
		}
	}()

	_, ok := EventToRequestTypeMapping[msg.Event]
	if !ok {
		msg.ErrorMessage = fmt.Sprintf("unknown event: %s", msg.Event)
		return
	}

	// Parse event
	isParsed, _, _, request, err := ParseEvent(msg.Event, []byte(msg.RequestPayload))
	if !isParsed || err != nil {
		msg.ErrorMessage = fmt.Sprintf("failed to parse event: %v", err)
		return
	}

	// Convert request to request type
	replyJSON, err := request.Process(db)
	if err != nil {
		msg.ErrorMessage = fmt.Sprintf("failed to process request: %v", err)
	} else {
		msg.Success = true
		if replyJSON != nil {
			msg.ResponsePayload = string(replyJSON)
		}
	}
}
