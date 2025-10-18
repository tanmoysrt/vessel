package main

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"slices"
)

func getTLSCertificateID(domain string, isWildCard bool) string {
	if isWildCard {
		return fmt.Sprintf("*.%s", domain)
	}
	return domain
}

func isTLSCertificateExist(db *gorm.DB, id string) (bool, error) {
	var cert TLSCertificate
	err := db.Where("id = ?", id).First(&cert).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func getTLSCertByID(db *gorm.DB, id string) (*TLSCertificate, error) {
	var cert TLSCertificate
	err := db.Where("id = ?", id).First(&cert).Error
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

func isListenerExist(db *gorm.DB, id string) (bool, error) {
	var listener Listener
	err := db.Where("id = ?", id).First(&listener).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func getListenerByID(db *gorm.DB, id string) (*Listener, error) {
	var listener Listener
	err := db.Where("id = ?", id).First(&listener).Error
	if err != nil {
		return nil, err
	}
	return &listener, nil
}

func getListenerID(bindIP string, port int) string {
	return fmt.Sprintf("%s:%d", bindIP, port)
}

func upsertListener(db *gorm.DB, bindIP string, port int, protocol ProtocolType, isTLS bool) (*Listener, error) {
	id := getListenerID(bindIP, port)

	isExist, err := isListenerExist(db, id)
	if err != nil {
		return nil, err
	}
	if isExist {
		//	Validate the TLS config
		listener, err := getListenerByID(db, id)
		if err != nil {
			return nil, err
		}

		// Protocol conflict check
		if listener.Protocol != protocol {
			return nil, fmt.Errorf("listener registered on %s is using %s protocol, but currently requesting the same listener for %s protocol. remove existing ingress / redirect rules to release the listener", id, listener.Protocol, protocol)
		}

		// TLS conflict check
		if listener.IsTLS != isTLS {
			if listener.IsTLS {
				return nil, fmt.Errorf("listener registered on %s is using TLS, but currently requesting the same listener for non-TLS. remove existing ingress / redirect rules to release the listener", id)
			} else {
				return nil, fmt.Errorf("listener registered on %s is using non-TLS, but currently requesting the same listener for TLS. remove existing ingress / redirect rules to release the listener", id)
			}
		}

		// Everything else is fine
		return listener, nil
	}

	//	Create the entry
	listener := &Listener{
		ID:       id,
		Protocol: protocol,
		IP:       bindIP,
		Port:     port,
		IsTLS:    isTLS,
	}
	return listener, db.Create(listener).Error
}

func findBackend(db *gorm.DB, resolverType BackendResolverType, dnsResolver string, hosts StringList, port int, isTLS bool, sniDomain string) (*Backend, error) {
	hostsValue, err := hosts.Value()
	if err != nil {
		return nil, err
	}

	var backend Backend
	err = db.Where("resolver_type = ? AND dns_resolver = ? AND hosts = ? AND port = ? AND is_tls = ? AND sni_domain = ?", resolverType, dnsResolver, hostsValue, port, isTLS, sniDomain).First(&backend).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &backend, nil
}

func upsertBackend(db *gorm.DB, resolverType BackendResolverType, dnsResolver string, hosts []string, port int, isTLS bool, sniDomain string) (*Backend, error) {
	// Check if backend exists
	backend, err := findBackend(db, resolverType, dnsResolver, hosts, port, isTLS, sniDomain)
	if err != nil {
		return nil, err
	}

	if backend != nil {
		// Backend exists, with same config
		return backend, nil
	}

	//	Create the entry
	backend = &Backend{
		ID:           uuid.NewString(),
		ResolverType: resolverType,
		DNSResolver:  dnsResolver,
		Hosts:        hosts,
		Port:         port,
		IsTLS:        isTLS,
		SNIDomain:    sniDomain,
	}
	return backend, db.Create(backend).Error
}

func getIngressRuleID(protocol ProtocolType, listenerID string, domain string, routePrefix string) string {
	if protocol == TCP {
		return fmt.Sprintf("tcp:%s", listenerID)
	} else {
		return fmt.Sprintf("http:%s:%s:%s", listenerID, domain, routePrefix)
	}
}

func findIngressRule(db *gorm.DB, protocol ProtocolType, listenerID string, domain string, routePrefix string) (*IngressRule, error) {
	id := getIngressRuleID(protocol, listenerID, domain, routePrefix)
	var rule IngressRule
	err := db.Where("id = ?", id).First(&rule).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

func upsertIngressRule(db *gorm.DB, protocol ProtocolType, listenerID string, domain string, routePrefix string, backendID string, allowedCIDRs StringList, deniedCIDRs StringList, priority int) (*IngressRule, error) {
	//	Try to find existing ingress rule
	ingressRule, err := findIngressRule(db, protocol, listenerID, domain, routePrefix)
	if err != nil {
		return nil, err
	}

	// Update the existing record
	if ingressRule != nil {
		ingressRule.BackendID = backendID
		ingressRule.AllowedCIDRs = allowedCIDRs
		ingressRule.DeniedCIDRs = deniedCIDRs
		ingressRule.Priority = priority
		return ingressRule, db.Save(ingressRule).Error
	}

	//	Create a new record
	ingressRule = &IngressRule{
		ID:           getIngressRuleID(protocol, listenerID, domain, routePrefix),
		Priority:     priority,
		ListenerID:   listenerID,
		BackendID:    backendID,
		Domain:       domain,
		RoutePrefix:  routePrefix,
		AllowedCIDRs: allowedCIDRs,
		DeniedCIDRs:  deniedCIDRs,
	}

	fmt.Println("Error after this point")

	return ingressRule, db.Create(ingressRule).Error
}

func deleteIngressRule(db *gorm.DB, protocol ProtocolType, listenerID string, domain string, routePrefix string) error {
	id := getIngressRuleID(protocol, listenerID, domain, routePrefix)
	return db.Where("id = ?", id).Delete(&IngressRule{}).Error
}

func getHTTPRedirectRuleID(listenerID string, domain string, routePrefix string, isHTTPSRedirect bool) string {
	redirectType := "https"
	if !isHTTPSRedirect {
		redirectType = "other"
	}
	return fmt.Sprintf("http:%s:%s:%s:%d", listenerID, domain, routePrefix, redirectType)
}

func findHTTPRedirectRule(db *gorm.DB, listenerID string, domain string, routePrefix string, isHTTPSRedirect bool) (*HTTPRedirectRule, error) {
	id := getHTTPRedirectRuleID(listenerID, domain, routePrefix, isHTTPSRedirect)
	var rule HTTPRedirectRule
	err := db.Where("id = ?", id).First(&rule).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

func upsertHTTPRedirectRule(db *gorm.DB, listenerID string, domain string, routePrefix string, isHttpsRedirect bool, schemeRedirect string, hostRedirect string, pathRedirect string, statusCode int, priority int) (*HTTPRedirectRule, error) {
	// Try to find existing ingress rule
	redirectRule, err := findHTTPRedirectRule(db, listenerID, domain, routePrefix, isHttpsRedirect)
	if err != nil {
		return nil, err
	}

	if statusCode == 0 {
		statusCode = 301
	}

	// Update the existing record
	if redirectRule != nil {
		redirectRule.IsHttpsRedirect = isHttpsRedirect
		redirectRule.SchemeRedirect = schemeRedirect
		redirectRule.HostRedirect = hostRedirect
		redirectRule.PathRedirect = pathRedirect
		redirectRule.StatusCode = statusCode
		return redirectRule, db.Save(redirectRule).Error
	}

	// Create a new record
	redirectRule = &HTTPRedirectRule{
		ID:              getHTTPRedirectRuleID(listenerID, domain, routePrefix, isHttpsRedirect),
		Priority:        priority,
		ListenerID:      listenerID,
		Domain:          domain,
		PathPrefix:      routePrefix,
		IsHttpsRedirect: isHttpsRedirect,
		SchemeRedirect:  schemeRedirect,
		HostRedirect:    hostRedirect,
		PathRedirect:    pathRedirect,
		StatusCode:      statusCode,
	}

	return redirectRule, db.Create(redirectRule).Error
}

func deleteHTTPRedirectRule(db *gorm.DB, listenerID string, domain string, routePrefix string, isHTTPSRedirect bool) error {
	id := getHTTPRedirectRuleID(listenerID, domain, routePrefix, isHTTPSRedirect)
	return db.Where("id = ?", id).Delete(&HTTPRedirectRule{}).Error
}

func cleanupUnusedBackendsAndListeners(db *gorm.DB) error {
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
	allBackendIDs = uniqueSortedStrings(allBackendIDs)
	allListenerIDs = uniqueSortedStrings(allListenerIDs)

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
