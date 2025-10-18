package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"time"
)

var EventToRequestTypeMapping = map[string]reflect.Type{
	"v1.tls_certificate.upsert":    reflect.TypeOf(TLSCertificateUpsertV1{}),
	"v1.tls_certificate.delete":    reflect.TypeOf(TLSCertificateDeleteV1{}),
	"v1.ingress_rule.upsert":       reflect.TypeOf(IngressRuleUpsertV1{}),
	"v1.ingress_rule.delete":       reflect.TypeOf(IngressRuleDeleteV1{}),
	"v1.http_redirect_rule.upsert": reflect.TypeOf(HTTPRedirectRuleUpsertV1{}),
	"v1.http_redirect_rule.delete": reflect.TypeOf(HTTPRedirectRuleDeleteV1{}),
}

func parseEvent(event string, data []byte) (isParsed bool, requestID string, requestedAt *time.Time, message MessageInterface, err error) {
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
		message = request.(MessageInterface)
	} else {
		isParsed = false
		err = errors.New("unknown event: " + event)
	}
	return
}

func processMessage(db *gorm.DB, msg *Message) {
	// Set current time
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
	isParsed, _, _, request, err := parseEvent(msg.Event, []byte(msg.RequestPayload))
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

// NOTE: In case of error as well, don't expect that transaction will be rolled back.
// It's up to the `Process` function to handle the error and rollback the changes.
// The raised error will be propagated to the client.

func (r *TLSCertificateUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	if r.Domain == "" {
		return nil, errors.New("domain is required")
	}
	if r.Cert == "" || r.Key == "" {
		return nil, errors.New("cert and key are required")
	}

	r.Cert = strings.ReplaceAll(r.Cert, "\\n", "\n")
	r.Key = strings.ReplaceAll(r.Key, "\\n", "\n")

	// Add \n to the end of cert and key if not present
	if !strings.HasSuffix(r.Cert, "\n") {
		r.Cert += "\n"
	}
	if !strings.HasSuffix(r.Key, "\n") {
		r.Key += "\n"
	}

	// Find the existing record first by domain and is_wildcard
	id := getTLSCertificateID(r.Domain, r.IsWildcard)
	isExist, err := isTLSCertificateExist(db, id)
	if err != nil {
		return nil, err
	}

	// Validate TLS certificate
	if err = ValidateCertAndKey(r.Cert, r.Key); err != nil {
		return nil, err
	}

	// Find expiry of the certificate
	certExpiry, err := getCertExpiry(r.Cert)
	if err != nil {
		return nil, err
	}

	// Update in DB
	certificateRecord := TLSCertificate{
		ID:         id,
		Domain:     r.Domain,
		IsWildcard: r.IsWildcard,
		Cert:       r.Cert,
		Key:        r.Key,
		ExpiresAt:  certExpiry,
	}

	if isExist {
		err = db.Save(&certificateRecord).Error
	} else {
		err = db.Create(&certificateRecord).Error
	}

	// Marshal the record to JSON
	jsonStr, err := json.Marshal(certificateRecord)
	if err != nil {
		return nil, err
	}

	return jsonStr, nil
}

func (r *TLSCertificateDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	id := getTLSCertificateID(r.Domain, r.IsWildcard)
	isExist, err := isTLSCertificateExist(db, id)
	if err != nil {
		return nil, err
	}
	if !isExist {
		return nil, nil
	}
	err = db.Delete(&TLSCertificate{ID: id}).Error
	return nil, err
}

func (r *IngressRuleUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	// Payload Validation
	if r.BindIP != "0.0.0.0" {
		return nil, errors.New("currently only 0.0.0.0 is supported for bind_ip")
	}

	if r.Port <= 0 || r.Port > 65535 {
		return nil, errors.New("port is required and must be between 1 and 65535")
	}

	if (r.Protocol == HTTP || r.IsTLS) && r.Domain == "" {
		return nil, errors.New("domain is required for HTTP protocol")
	}

	// Validate domain if HTTP protocol or TLS enabled
	if r.Protocol == HTTP || r.IsTLS {
		if isValid, err := validateDomain(r.Domain); !isValid {
			return nil, fmt.Errorf("invalid domain: %w", err)
		}
	}

	if r.Protocol == HTTP {
		if r.RoutePrefix == "" {
			r.RoutePrefix = "/"
		}
	}

	// Backend validation
	if len(r.BackendHosts) == 0 {
		return nil, errors.New("at least one backend host is required")
	}

	// Resolver Validation
	if r.BackendResolver == DNS_RESOLVER && r.BackendDNSResolver == "" {
		return nil, errors.New("backend_dns_resolver is required for DNS resolver")
	}

	// CIDR validation
	for _, cidr := range r.AllowedCIDRs {
		if !IsValidCIDR(cidr) {
			return nil, fmt.Errorf("invalid cidr: %s in allowed_cidrs list", cidr)
		}
	}

	for _, cidr := range r.DeniedCIDRs {
		if !IsValidCIDR(cidr) {
			return nil, fmt.Errorf("invalid cidr: %s in denied_cidrs list", cidr)
		}
	}

	// Create / Update the listener
	listener, err := upsertListener(db, r.BindIP, r.Port, r.Protocol, r.IsTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert listener: %w", err)
	}

	// Create / Update the backend
	backend, err := upsertBackend(db, r.BackendResolver, r.BackendDNSResolver, r.BackendHosts, r.BackendPort, r.IsTLS, r.BackendSNIDomain)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert backend: %w", err)
	}

	// Create / Update the ingress rule
	ingressRule, err := upsertIngressRule(db, r.Protocol, listener.ID, r.Domain, r.RoutePrefix, backend.ID, r.AllowedCIDRs, r.DeniedCIDRs, r.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert ingress rule: %w", err)
	}

	// Marshal the record to JSON
	jsonStr, err := json.Marshal(ingressRule)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ingress rule payload : %w", err)
	}
	return jsonStr, nil
}

func (r *IngressRuleDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	// Payload Validation
	if r.BindIP != "0.0.0.0" {
		return nil, errors.New("currently only 0.0.0.0 is supported for bind_ip")
	}

	if r.Port <= 0 || r.Port > 65535 {
		return nil, errors.New("port is required and must be between 1 and 65535")
	}

	// Validate domain if HTTP protocol
	if r.Protocol == HTTP {
		if isValid, err := validateDomain(r.Domain); !isValid {
			return nil, fmt.Errorf("invalid domain: %w", err)
		}
	}

	if r.Protocol == HTTP {
		if r.RoutePrefix == "" {
			r.RoutePrefix = "/"
		}
	}

	err := deleteIngressRule(db, r.Protocol, getListenerID(r.BindIP, r.Port), r.Domain, r.RoutePrefix)
	return nil, err
}

func (r *HTTPRedirectRuleUpsertV1) Process(db *gorm.DB) (json.RawMessage, error) {
	// Payload Validation
	if r.BindIP != "0.0.0.0" {
		return nil, errors.New("currently only 0.0.0.0 is supported for bind_ip")
	}

	if r.Port <= 0 || r.Port > 65535 {
		return nil, errors.New("port is required and must be between 1 and 65535")
	}

	// Validate domain
	if isValid, err := validateDomain(r.Domain); !isValid {
		return nil, fmt.Errorf("invalid domain: %w", err)
	}

	if r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	// Create / Update the listener
	listener, err := upsertListener(db, r.BindIP, r.Port, HTTP, r.IsTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert listener: %w", err)
	}

	// Create / Update the redirect rule
	redirectRule, err := upsertHTTPRedirectRule(db, listener.ID, r.Domain, r.RoutePrefix, r.IsHttpsRedirect, r.SchemeRedirect, r.HostRedirect, r.PathRedirect, r.StatusCode, r.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert redirect rule: %w", err)
	}

	// Marshal the record to JSON
	jsonStr, err := json.Marshal(redirectRule)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal redirect rule payload : %w", err)
	}
	return jsonStr, nil
}

func (r *HTTPRedirectRuleDeleteV1) Process(db *gorm.DB) (json.RawMessage, error) {
	// Payload Validation
	if r.BindIP != "0.0.0.0" {
		return nil, errors.New("currently only 0.0.0.0 is supported for bind_ip")
	}

	if r.Port <= 0 || r.Port > 65535 {
		return nil, errors.New("port is required and must be between 1 and 65535")
	}

	// Validate domain
	if isValid, err := validateDomain(r.Domain); !isValid {
		return nil, fmt.Errorf("invalid domain: %w", err)
	}

	if r.RoutePrefix == "" {
		r.RoutePrefix = "/"
	}

	return nil, deleteHTTPRedirectRule(db, getListenerID(r.BindIP, r.Port), r.Domain, r.RoutePrefix, r.IsHttpsRedirect)
}
