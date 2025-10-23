package main

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"net"
	"slices"
	"strings"
)

// Some Guides & Assumptions for the following methods:
// 1. There are majorly two kinds of operations.
//	    a. UPSERT - Insert or update the record based on the given parameters.
//	    b. DELETE - Delete the record based on the given parameters.
// 2. Do required validations before doing changes on the database.
// 3. Don't do changes in the database layer until unless everything is validated.
//    Because those partial db changes will be committed.
//	  Or, you can manually revert the changes if something goes wrong.
// 4. Most of the time no need to raise a specific validation error,
//    because it's the responsibility of the caller to do better validation.
// 5. Don't set any default value for any fields,
//    because it's the responsibility of the caller to do that.

// ================================
// TLS Certificate Related Methods
// ================================

// GetTLSCertificateID returns the ID to be used for the TLS certificate.
func GetTLSCertificateID(domain string, isWildCard bool) string {
	if isWildCard {
		return fmt.Sprintf("*.%s", domain)
	}
	return domain
}

// UpsertTLSCertificate creates a new TLS certificate record if it doesn't exist or updates the existing record if it exists.
// Additionally, it validates the certificate and key for the correct PEM format.
func UpsertTLSCertificate(db *gorm.DB, domain string, isWildCard bool, cert string, key string) (*TLSCertificate, error) {
	tlsCertificateID := GetTLSCertificateID(domain, isWildCard)

	if domain == "" || cert == "" || key == "" {
		return nil, errors.New("domain, cert and key are required")
	}

	// Validate the domain name
	if strings.HasPrefix(domain, "*.") {
		return nil, errors.New("*. can't be part of domain name, instead use is_wildcard flag to request")
	}

	isValidDomain, err := IsValidDomain(domain)
	if !isValidDomain {
		if err != nil {
			return nil, fmt.Errorf("invalid domain name: %w", err)
		}
		return nil, errors.New("invalid domain name due to unknown reason")
	}

	// Validate PEM certificate and keys
	cert = strings.ReplaceAll(cert, "\\n", "\n")
	key = strings.ReplaceAll(key, "\\n", "\n")

	// Add \n to the end of cert and key if not present
	if !strings.HasSuffix(cert, "\n") {
		cert += "\n"
	}
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}

	isExist, err := recordExists(db, &TLSCertificate{}, tlsCertificateID)
	if err != nil {
		return nil, err
	}

	// Validate TLS certificate
	if err = ValidateCertAndKey(cert, key); err != nil {
		return nil, err
	}

	// Find expiry of the certificate
	certExpiry, err := GetCertExpiry(cert)
	if err != nil {
		return nil, err
	}

	certificateRecord := TLSCertificate{
		ID:         tlsCertificateID,
		Domain:     domain,
		IsWildcard: isWildCard,
		Cert:       cert,
		Key:        key,
		ExpiresAt:  certExpiry,
	}

	if isExist {
		return &certificateRecord, db.Save(&certificateRecord).Error
	} else {
		return &certificateRecord, db.Create(&certificateRecord).Error
	}
}

// =========================
// Listener Related Methods
// =========================

// GetListenerID returns the ID to be used for the listener
func GetListenerID(bindIP string, port int) string {
	return fmt.Sprintf("%s|%d", bindIP, port)
}

// UpsertListener creates a new listener record if it doesn't exist or updates the existing record if it exists.
func UpsertListener(db *gorm.DB, bindIP string, port int, protocol ProtocolType, isTLS bool) (*Listener, error) {
	// Validate Bind IP
	// We can later utilize it to bind listener to a specific interface.
	if bindIP != "0.0.0.0" && bindIP != "127.0.0.1" {
		return nil, errors.New("currently only 0.0.0.0 and 127.0.0.1 is supported for bind_ip")
	}

	// Validate port number
	if !(port >= 1 && port <= 65535) {
		return nil, errors.New("port must be between 1 and 65535")
	}

	// Validate ipv4 / v6 address
	if IsValidIPV4(bindIP) == false && IsValidIPV6(bindIP) == false {
		return nil, errors.New("invalid IP address")
	}

	listenerID := GetListenerID(bindIP, port)

	// Check if listener exists
	isExist, err := recordExists(db, &Listener{}, listenerID)
	if err != nil {
		return nil, err
	}
	if isExist {
		var listener Listener

		//	Validate the TLS config
		err = db.Where("id = ?", listenerID).First(&listener).Error
		if err != nil {
			return nil, err
		}

		// Protocol conflict check
		if listener.Protocol != protocol {
			return nil, fmt.Errorf("listener registered on %s is using %s protocol, but currently requesting the same listener for %s protocol. remove existing ingress / redirect rules to release the listener", listenerID, listener.Protocol, protocol)
		}

		// TLS conflict check
		// If the listener is already using TLS, then we cannot change it to non-TLS.
		// We need to purge all ingress / redirect rules that are using this listener to release the listener.
		if listener.IsTLS != isTLS {
			if listener.IsTLS {
				return nil, fmt.Errorf("listener registered on %s is using TLS, but currently requesting the same listener for non-TLS. remove existing ingress / redirect rules to release the listener", listenerID)
			} else {
				return nil, fmt.Errorf("listener registered on %s is using non-TLS, but currently requesting the same listener for TLS. remove existing ingress / redirect rules to release the listener", listenerID)
			}
		}

		// Listener exists, with the same config
		return &listener, nil
	}

	// Insert the new listener record
	listener := &Listener{
		ID:       listenerID,
		Protocol: protocol,
		IP:       bindIP,
		Port:     port,
		IsTLS:    isTLS,
	}
	return listener, db.Create(listener).Error
}

// ================================
// Backend Service Related Methods
// ===============================

// FindBackend helps in finding the backend service record based on the given parameters.
func FindBackend(db *gorm.DB, resolverType BackendResolverType, dnsResolver string, hosts StringList, port int, isTLS bool, sniDomain string, proxyProtocolVersion ProxyProtocolVersion) (*Backend, error) {
	hostsValue, err := hosts.Value()
	if err != nil {
		return nil, err
	}

	var backend Backend
	err = db.Where("resolver_type = ? AND dns_resolver = ? AND hosts = ? AND port = ? AND is_tls = ? AND sni_domain = ? AND proxy_protocol_version = ?", resolverType, dnsResolver, hostsValue, port, isTLS, sniDomain, proxyProtocolVersion).First(&backend).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &backend, nil
}

// UpsertBackend creates a new backend service record if it doesn't exist or updates the existing record if it exists.
func UpsertBackend(db *gorm.DB, resolverType BackendResolverType, dnsResolver string, hosts StringList, port int, isTLS bool, sniDomain string, proxyProtocolVersion ProxyProtocolVersion) (*Backend, error) {
	// Validate port number
	if !(port >= 1 && port <= 65535) {
		return nil, errors.New("port must be between 1 and 65535")
	}

	// In the case of dns resolver, we need to have exactly one host
	if resolverType == DnsResolver && len(hosts) != 1 {
		return nil, errors.New("dns resolver requires exactly one host")
	}

	// Check if the backend with the same config already exists
	backend, err := FindBackend(db, resolverType, dnsResolver, hosts, port, isTLS, sniDomain, proxyProtocolVersion)
	if err != nil {
		return nil, err
	}

	// Backend exists, with the same config
	if backend != nil {
		return backend, nil
	}

	// Insert the new backend record
	// We don't need to care about the existing record, because unused backend will be deleted by pruneOrphanedResources()
	backend = &Backend{
		ID:                   uuid.NewString(),
		ResolverType:         resolverType,
		DNSResolver:          dnsResolver,
		Hosts:                hosts,
		Port:                 port,
		IsTLS:                isTLS,
		SNIDomain:            sniDomain,
		ProxyProtocolVersion: proxyProtocolVersion,
	}
	return backend, db.Create(backend).Error
}

// =============================
// Ingress Rule Related Methods
// =============================

// GetIngressRuleID returns the ID to be used for the ingress rule
func GetIngressRuleID(protocol ProtocolType, listenerID string, domain string, routePrefix string) string {
	if protocol == TCP {
		return fmt.Sprintf("tcp|%s", listenerID)
	} else {
		return fmt.Sprintf("http|%s|%s|%s", listenerID, domain, routePrefix)
	}
}

// UpsertIngressRule creates a new ingress rule record if it doesn't exist or updates the existing record if it exists.
func UpsertIngressRule(db *gorm.DB, protocol ProtocolType, listenerID string, domain string, routePrefix string, backendID string, allowedCIDRs StringList, deniedCIDRs StringList, priority int) (*IngressRule, error) {
	// Validate the parameters
	if protocol == HTTP {
		if domain == "" {
			return nil, errors.New("domain is required for HTTP ingress rule")
		} else if routePrefix == "" {
			return nil, errors.New("route prefix is required for HTTP ingress rule")
		}

		// Validate the domain name
		isValid, err := IsValidDomain(domain)
		if !isValid {
			if err != nil {
				return nil, fmt.Errorf("invalid domain name: %w", err)
			}
			return nil, errors.New("invalid domain name due to unknown reason")
		}
	}

	// Validate the CIDRs
	for _, cidr := range allowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, fmt.Errorf("invalid CIDR: %w", err)
		}
	}
	for _, cidr := range deniedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, fmt.Errorf("invalid CIDR: %w", err)
		}
	}

	ingressRuleID := GetIngressRuleID(protocol, listenerID, domain, routePrefix)

	// Check if the ingress rule already exists
	isExist, err := recordExists(db, &IngressRule{}, ingressRuleID)
	if err != nil {
		return nil, err
	}

	ingressRule := &IngressRule{
		ID:           ingressRuleID,
		Priority:     priority,
		ListenerID:   listenerID,
		BackendID:    backendID,
		Domain:       domain,
		RoutePrefix:  routePrefix,
		AllowedCIDRs: allowedCIDRs,
		DeniedCIDRs:  deniedCIDRs,
	}

	// Either update or insert the record
	if isExist {
		return ingressRule, db.Save(ingressRule).Error
	} else {
		return ingressRule, db.Create(ingressRule).Error
	}
}

// DeleteIngressRule deletes the ingress rule record based on the given parameters.
func DeleteIngressRule(db *gorm.DB, protocol ProtocolType, listenerID string, domain string, routePrefix string) error {
	id := GetIngressRuleID(protocol, listenerID, domain, routePrefix)
	return db.Where("id = ?", id).Delete(&IngressRule{}).Error
}

// ==================================
// HTTP Redirect Rule Related Methods
// ==================================

// GetHTTPRedirectRuleID returns the ID to be used for the HTTP redirect rule
func GetHTTPRedirectRuleID(listenerID string, domain string, routePrefix string) string {
	return fmt.Sprintf("http|%s|%s|%s", listenerID, domain, routePrefix)
}

// UpsertHTTPRedirectRule creates a new HTTP redirect rule record if it doesn't exist or updates the existing record if it exists.
func UpsertHTTPRedirectRule(db *gorm.DB, listenerID string, domain string, routePrefix string, schemeRedirect string, hostRedirect string, pathRedirect string, statusCode int, priority int) (*HTTPRedirectRule, error) {
	if domain == "" {
		return nil, errors.New("domain is required for HTTP redirect rule")
	}
	if routePrefix == "" {
		return nil, errors.New("route prefix is required for HTTP redirect rule")
	}
	if schemeRedirect == "" && hostRedirect == "" && pathRedirect == "" {
		return nil, errors.New("scheme, host or path redirect is required for HTTP redirect rule")
	}

	if schemeRedirect != "http" && schemeRedirect != "https" {
		return nil, errors.New("scheme redirect must be http or https")
	}

	if statusCode != 301 && statusCode != 302 && statusCode != 303 && statusCode != 307 && statusCode != 308 {
		return nil, errors.New("status code must be 301, 302, 303, 307 or 308")
	}

	// Validate the domain name
	isValid, err := IsValidDomain(domain)
	if !isValid {
		if err != nil {
			return nil, fmt.Errorf("invalid domain name: %w", err)
		}
		return nil, errors.New("invalid domain name due to unknown reason")
	}

	httpRedirectRuleID := GetHTTPRedirectRuleID(listenerID, domain, routePrefix)

	// Check if the HTTP redirect rule already exists
	isExist, err := recordExists(db, &HTTPRedirectRule{}, httpRedirectRuleID)
	if err != nil {
		return nil, err
	}

	httpRedirectRule := &HTTPRedirectRule{
		ID:             httpRedirectRuleID,
		Priority:       priority,
		ListenerID:     listenerID,
		Domain:         domain,
		PathPrefix:     routePrefix,
		SchemeRedirect: schemeRedirect,
		HostRedirect:   hostRedirect,
		PathRedirect:   pathRedirect,
		StatusCode:     statusCode,
	}

	// Either update or insert the record
	if isExist {
		return httpRedirectRule, db.Save(httpRedirectRule).Error
	} else {
		return httpRedirectRule, db.Create(httpRedirectRule).Error
	}
}

// DeleteHTTPRedirectRule deletes the HTTP redirect rule record based on the given parameters.
func DeleteHTTPRedirectRule(db *gorm.DB, listenerID string, domain string, routePrefix string) error {
	id := GetHTTPRedirectRuleID(listenerID, domain, routePrefix)
	return db.Where("id = ?", id).Delete(&HTTPRedirectRule{}).Error
}

// =====================
// Other Utility Methods
// =====================

// recordExists checks if the given record exists in the database.
// It returns true if the record exists, false otherwise.
// It also returns an error if the database operation fails.
func recordExists(db *gorm.DB, model interface{}, id string) (bool, error) {
	err := db.Select("id").Where("id = ?", id).First(model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// pruneOrphanedResources removes unused backend and listener records from the database to prevent resource leaks.
// It identifies orphaned resources by comparing IDs in IngressRule and HTTPRedirectRule tables with existing resources.
// The function ensures only referenced resources remain while unused ones are purged.
func pruneOrphanedResources(db *gorm.DB) error {
	//	Find listener_id from Ingress and Redirect Rule
	var listenerIDsFromIngressRules []string
	var listenerIDsFromHTTPRedirectRules []string

	if err := db.Model(&IngressRule{}).Pluck("listener_id", &listenerIDsFromIngressRules).Error; err != nil {
		return fmt.Errorf("failed to get listener_id from Ingress Rule: %w", err)
	}

	if err := db.Model(&HTTPRedirectRule{}).Pluck("listener_id", &listenerIDsFromHTTPRedirectRules).Error; err != nil {
		return fmt.Errorf("failed to get listener_id from HTTP Redirect Rule: %w", err)
	}

	allListenerIDs := slices.Concat(listenerIDsFromIngressRules, listenerIDsFromHTTPRedirectRules)

	// Find backend_id from Ingress Rule
	var allBackendIDs []string
	if err := db.Model(&IngressRule{}).Pluck("backend_id", &allBackendIDs).Error; err != nil {
		return fmt.Errorf("failed to get backend_id from Ingress Rule: %w", err)
	}

	// Remove duplicates from both
	allBackendIDs = UniqueSortedStrings(allBackendIDs)
	allListenerIDs = UniqueSortedStrings(allListenerIDs)

	//	Remove these backend and listener from the database
	if len(allBackendIDs) > 0 {
		err := db.Where("id NOT IN (?)", allBackendIDs).Delete(&Backend{}).Error
		if err != nil {
			return fmt.Errorf("failed to delete unused backends: %w", err)
		}
	}
	if len(allListenerIDs) > 0 {
		err := db.Where("id NOT IN (?)", allListenerIDs).Delete(&Listener{}).Error
		if err != nil {
			return fmt.Errorf("failed to delete unused listeners: %w", err)
		}
	}
	return nil
}
