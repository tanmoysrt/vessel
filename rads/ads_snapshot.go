package main

import (
	"fmt"
	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	rbac "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	rbachttp "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
	router "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	proxyProtocolListener "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/proxy_protocol/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	rbacnet "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/rbac/v3"
	tcpProxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	proxyProtocolUpstream "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
	rawbuffer "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/raw_buffer/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"sort"
	"time"
)

// Future Refactor:
// At some places, used for loop to find some specific element
// Maybe better to put all resources in a map and use that to find a specific resource

func GenerateSnapshot(
	numOfTrustedHops int,
	enableDownstreamProxyProtocol bool,
	listeners []Listener,
	backends []Backend,
	ingressRules []IngressRule,
	redirectRules []HTTPRedirectRule,
	tlsCerts []TLSCertificate,
) (version string, snapshot *cache.Snapshot, err error) {
	// Sort the ingress rules by Priority (DESC)
	sort.Slice(ingressRules, func(i, j int) bool {
		return ingressRules[i].Priority > ingressRules[j].Priority
	})
	// Sort the redirect rules by Priority (DESC)
	sort.Slice(redirectRules, func(i, j int) bool {
		return redirectRules[i].Priority > redirectRules[j].Priority
	})

	sg := &SnapshotGenerator{
		version:                       fmt.Sprintf("%d", time.Now().Unix()),
		numOfTrustedHops:              numOfTrustedHops,
		enableDownstreamProxyProtocol: enableDownstreamProxyProtocol,
		listeners:                     listeners,
		backends:                      backends,
		ingressRules:                  ingressRules,
		redirectRules:                 redirectRules,
		tlsCerts:                      tlsCerts,
	}
	snapshot, err = sg.Generate()
	version = sg.version
	return
}

// Generate creates a complete Envoy snapshot from your data models
func (sg *SnapshotGenerator) Generate() (*cache.Snapshot, error) {
	snapshot, err := cache.NewSnapshot(
		sg.version,
		map[resource.Type][]types.Resource{
			// LDS - Listener Discovery Service
			// https://www.envoyproxy.io/docs/envoy/latest/configuration/listeners/lds
			resource.ListenerType: sg.buildListeners(),
			// CDS - Cluster Discovery Service
			// https://www.envoyproxy.io/docs/envoy/latest/configuration/upstream/cluster_manager/cds
			resource.ClusterType: sg.buildClusters(),
			// EDS - Endpoint Discovery Service
			// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/service_discovery
			resource.EndpointType: sg.buildEndpoints(),
			// RDS - Route Discovery Service
			// https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_conn_man/rds
			resource.RouteType: sg.buildRoutes(),
			// SDS - Secret Discovery Service
			// https://www.envoyproxy.io/docs/envoy/latest/configuration/security/secret
			resource.SecretType: sg.buildSecrets(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate snapshot: %w", err)
	}

	return snapshot, nil
}

// buildListeners creates Envoy listeners from your Listener model
// https://www.envoyproxy.io/docs/envoy/latest/configuration/listeners/lds
func (sg *SnapshotGenerator) buildListeners() []types.Resource {
	var resources []types.Resource
	var filterChain *listener.FilterChain

	for _, l := range sg.listeners {
		if l.Protocol == HTTP {
			routeConfigName := fmt.Sprintf("route_%s", l.ID)

			// Create HTTP Connection Manager
			manager := &hcm.HttpConnectionManager{
				CodecType:  hcm.HttpConnectionManager_AUTO,
				StatPrefix: fmt.Sprintf("ingress_%s", l.ID),
				RouteSpecifier: &hcm.HttpConnectionManager_Rds{
					Rds: &hcm.Rds{
						ConfigSource: &core.ConfigSource{
							ResourceApiVersion: core.ApiVersion_V3,
							ConfigSourceSpecifier: &core.ConfigSource_Ads{
								Ads: &core.AggregatedConfigSource{},
							},
						},
						RouteConfigName: routeConfigName,
					},
				},
				HttpFilters: []*hcm.HttpFilter{
					{
						Name: "envoy.filters.http.rbac",
						ConfigType: &hcm.HttpFilter_TypedConfig{
							TypedConfig: MustMarshalAny(&rbachttp.RBAC{}),
						},
					},
					{
						Name: "envoy.filters.http.router",
						ConfigType: &hcm.HttpFilter_TypedConfig{
							TypedConfig: MustMarshalAny(&router.Router{}),
						},
					},
				},
			}

			manager.UseRemoteAddress = wrapperspb.Bool(true)
			manager.XffNumTrustedHops = uint32(sg.numOfTrustedHops)
			managerAny := MustMarshalAny(manager)

			filterChain = &listener.FilterChain{
				Filters: []*listener.Filter{
					{
						Name: "envoy.filters.network.http_connection_manager",
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: managerAny,
						},
					},
				},
			}
		} else if l.Protocol == TCP {
			// Find the backend with this listener
			var ingressRule *IngressRule
			for i := range sg.ingressRules {
				if sg.ingressRules[i].ListenerID == l.ID {
					ingressRule = &sg.ingressRules[i]
					break
				}
			}

			// If no backend found, skip it
			if ingressRule == nil {
				continue
			}

			// TCP Filters
			var filters []*listener.Filter

			// Build RBAC Rules filter for IP allowlisting and denylisting
			// Order of filter matters, RBAC filter should be executed before tcp proxy filter
			if rbacRules := sg.buildRBACRules(*ingressRule); rbacRules != nil {
				rbacPolicy := &rbacnet.RBAC{
					Rules:      rbacRules,
					StatPrefix: fmt.Sprintf("ingress_%s", l.ID),
				}
				filters = append(filters, &listener.Filter{
					Name: "envoy.filters.network.rbac",
					ConfigType: &listener.Filter_TypedConfig{
						TypedConfig: MustMarshalAny(rbacPolicy),
					},
				})
			}

			// TCP Proxy filter config
			tcpConfig := &tcpProxy.TcpProxy{
				StatPrefix: fmt.Sprintf("ingress_%s", l.ID),
				ClusterSpecifier: &tcpProxy.TcpProxy_Cluster{
					Cluster: fmt.Sprintf("cluster_%s", ingressRule.BackendID),
				},
			}

			filters = append(filters, &listener.Filter{
				Name: "envoy.filters.network.tcp_proxy",
				ConfigType: &listener.Filter_TypedConfig{
					TypedConfig: MustMarshalAny(tcpConfig),
				},
			})

			// Prepare tcp filter chain
			filterChain = &listener.FilterChain{
				Filters: filters,
			}
		} else {
			// This gateway is designed to handle TCP and HTTP protocols only
			// Anything else is not supported.
			// Panic to catch this error early on in development.
			panic(fmt.Sprintf("unsupported protocol: %s", l.Protocol))
		}

		// Add TLS if enabled
		if l.IsTLS {
			tlsContext := sg.buildDownstreamTLSContext()
			if tlsContext != nil {
				filterChain.TransportSocket = &core.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &core.TransportSocket_TypedConfig{
						TypedConfig: MustMarshalAny(tlsContext),
					},
				}
			}
		}

		envoyListener := &listener.Listener{
			Name: fmt.Sprintf("listener_%s", l.ID),
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Protocol: core.SocketAddress_TCP,
						Address:  l.IP,
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: uint32(l.Port),
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{filterChain},
		}

		// Add PROXY Protocol in chain, if it's enabled at bootstrapping config
		if sg.enableDownstreamProxyProtocol {
			envoyListener.ListenerFilters = []*listener.ListenerFilter{
				{
					Name: wellknown.ProxyProtocol,
					ConfigType: &listener.ListenerFilter_TypedConfig{
						TypedConfig: MustMarshalAny(&proxyProtocolListener.ProxyProtocol{
							AllowRequestsWithoutProxyProtocol: false,
						}),
					},
				},
			}
		}
		resources = append(resources, envoyListener)
	}

	return resources
}

// buildDownstreamTLSContext creates TLS context for listeners
func (sg *SnapshotGenerator) buildDownstreamTLSContext() *tls.DownstreamTlsContext {
	var tlsConfigs []*tls.SdsSecretConfig

	for _, cert := range sg.tlsCerts {
		tlsConfigs = append(tlsConfigs, &tls.SdsSecretConfig{
			Name: fmt.Sprintf("tls_cert_%s", cert.ID),
			SdsConfig: &core.ConfigSource{
				ResourceApiVersion: core.ApiVersion_V3,
				ConfigSourceSpecifier: &core.ConfigSource_Ads{
					Ads: &core.AggregatedConfigSource{},
				},
			},
		})
	}

	if len(tlsConfigs) == 0 {
		return nil
	}

	return &tls.DownstreamTlsContext{
		CommonTlsContext: &tls.CommonTlsContext{
			TlsCertificateSdsSecretConfigs: tlsConfigs,
			TlsParams: &tls.TlsParameters{
				TlsMinimumProtocolVersion: tls.TlsParameters_TLSv1_2,
			},
		},
	}
}

// buildRoutes creates Envoy route configurations
// https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_conn_man/rds
func (sg *SnapshotGenerator) buildRoutes() []types.Resource {
	var resources []types.Resource

	// Group rules by listener
	rulesByListener := make(map[string][]IngressRule)
	redirectsByListener := make(map[string][]HTTPRedirectRule)

	for _, rule := range sg.ingressRules {
		rulesByListener[rule.ListenerID] = append(rulesByListener[rule.ListenerID], rule)
	}

	for _, redirect := range sg.redirectRules {
		redirectsByListener[redirect.ListenerID] = append(redirectsByListener[redirect.ListenerID], redirect)
	}

	for _, l := range sg.listeners {
		routeConfigName := fmt.Sprintf("route_%s", l.ID)

		var virtualHosts []*route.VirtualHost

		// Group by domain
		ingressRules := make(map[string][]IngressRule)
		redirectRules := make(map[string][]HTTPRedirectRule)

		for _, rule := range rulesByListener[l.ID] {
			domain := rule.Domain
			if domain == "" {
				domain = "*"
			}
			ingressRules[domain] = append(ingressRules[domain], rule)
		}

		for _, redirect := range redirectsByListener[l.ID] {
			domain := redirect.Domain
			if domain == "" {
				domain = "*"
			}
			redirectRules[domain] = append(redirectRules[domain], redirect)
		}

		// Create virtual hosts
		processedDomains := make(map[string]bool)

		for domain, rules := range ingressRules {
			if processedDomains[domain] {
				continue
			}
			processedDomains[domain] = true

			vh := &route.VirtualHost{
				Name:    fmt.Sprintf("vhost_%s", domain),
				Domains: []string{domain},
			}

			// Add redirect routes
			for _, redirect := range redirectRules[domain] {
				vh.Routes = append(vh.Routes, sg.buildRedirectRoute(redirect))
			}

			// Add ingress routes
			for _, rule := range rules {
				vh.Routes = append(vh.Routes, sg.buildIngressRoute(rule))
			}

			virtualHosts = append(virtualHosts, vh)
		}

		// Handle redirects without corresponding ingress rules
		for domain := range redirectRules {
			if processedDomains[domain] {
				continue
			}

			vh := &route.VirtualHost{
				Name:    fmt.Sprintf("vhost_%s", domain),
				Domains: []string{domain},
			}

			for _, redirect := range redirectRules[domain] {
				vh.Routes = append(vh.Routes, sg.buildRedirectRoute(redirect))
			}

			virtualHosts = append(virtualHosts, vh)
		}

		routeConfig := &route.RouteConfiguration{
			Name:                     routeConfigName,
			VirtualHosts:             virtualHosts,
			IgnorePortInHostMatching: true,
		}

		resources = append(resources, routeConfig)
	}

	return resources
}

// buildIngressRoute creates a route for an ingress rule
func (sg *SnapshotGenerator) buildIngressRoute(rule IngressRule) *route.Route {
	envoyRoute := &route.Route{
		Match: &route.RouteMatch{
			PathSpecifier: &route.RouteMatch_Prefix{
				Prefix: rule.RoutePrefix,
			},
		},
		Action: &route.Route_Route{
			Route: &route.RouteAction{
				ClusterSpecifier: &route.RouteAction_Cluster{
					Cluster: fmt.Sprintf("cluster_%s", rule.BackendID),
				},
			},
		},
	}

	// Attempt to configure RBAC rules for ip allowlisting and blocklisting
	if rbacRules := sg.buildRBACRules(rule); rbacRules != nil {
		rbacPolicy := &rbachttp.RBACPerRoute{
			Rbac: &rbachttp.RBAC{
				Rules: rbacRules,
			},
		}
		if envoyRoute.TypedPerFilterConfig == nil {
			envoyRoute.TypedPerFilterConfig = make(map[string]*anypb.Any, 1)
		}
		envoyRoute.TypedPerFilterConfig["envoy.filters.http.rbac"] = MustMarshalAny(rbacPolicy)
	}
	return envoyRoute
}

// buildRBACRules generates RBAC rules for the provided IngressRule based on allowed and denied CIDRs.
func (sg *SnapshotGenerator) buildRBACRules(rule IngressRule) *rbac.RBAC {
	if len(rule.AllowedCIDRs) == 0 && len(rule.DeniedCIDRs) == 0 {
		return nil
	}

	allow := &rbac.Principal{Identifier: &rbac.Principal_Any{Any: true}}
	if len(rule.AllowedCIDRs) > 0 {
		allow = sg.principalOR(rule.AllowedCIDRs)
	}

	var denyNot *rbac.Principal
	if len(rule.DeniedCIDRs) > 0 {
		denyNot = &rbac.Principal{
			Identifier: &rbac.Principal_NotId{
				NotId: sg.principalOR(rule.DeniedCIDRs),
			},
		}
	}

	var finalPrincipal *rbac.Principal
	if denyNot != nil {
		finalPrincipal = &rbac.Principal{
			Identifier: &rbac.Principal_AndIds{
				AndIds: &rbac.Principal_Set{Ids: []*rbac.Principal{allow, denyNot}},
			},
		}
	} else {
		finalPrincipal = allow
	}

	return &rbac.RBAC{
		Action: rbac.RBAC_ALLOW,
		Policies: map[string]*rbac.Policy{
			"allow_and_not_deny": {
				Permissions: []*rbac.Permission{{Rule: &rbac.Permission_Any{Any: true}}},
				Principals:  []*rbac.Principal{finalPrincipal},
			},
		},
	}
}

// buildRBACPrincipal builds an RBAC principal list for a set of CIDR addresses
func (sg *SnapshotGenerator) buildRBACPrincipal(cidrs []string) []*rbac.Principal {
	principals := make([]*rbac.Principal, 0, len(cidrs))
	for _, cidr := range cidrs {
		ip, prefixLen := ParseCIDR(cidr)
		principals = append(principals, &rbac.Principal{
			Identifier: &rbac.Principal_RemoteIp{
				RemoteIp: &core.CidrRange{
					AddressPrefix: ip,
					PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(prefixLen)},
				},
			},
		})
	}
	return principals
}

// principalOR combines multiple CIDR-based rbac principals into a single OR principal to match source IPs
func (sg *SnapshotGenerator) principalOR(cidrs []string) *rbac.Principal {
	ids := sg.buildRBACPrincipal(cidrs)
	return &rbac.Principal{
		Identifier: &rbac.Principal_OrIds{
			OrIds: &rbac.Principal_Set{Ids: ids},
		},
	}
}

// buildRedirectRoute creates a redirect route
func (sg *SnapshotGenerator) buildRedirectRoute(redirect HTTPRedirectRule) *route.Route {
	redirectAction := &route.RedirectAction{}

	if redirect.SchemeRedirect != "" {
		redirectAction.SchemeRewriteSpecifier = &route.RedirectAction_SchemeRedirect{
			SchemeRedirect: redirect.SchemeRedirect,
		}
	}

	if redirect.HostRedirect != "" {
		redirectAction.HostRedirect = redirect.HostRedirect
	}

	if redirect.PathRedirect != "" {
		redirectAction.PathRewriteSpecifier = &route.RedirectAction_PathRedirect{
			PathRedirect: redirect.PathRedirect,
		}
	}

	switch redirect.StatusCode {
	case 301:
		redirectAction.ResponseCode = route.RedirectAction_MOVED_PERMANENTLY
	case 302:
		redirectAction.ResponseCode = route.RedirectAction_FOUND
	case 303:
		redirectAction.ResponseCode = route.RedirectAction_SEE_OTHER
	case 307:
		redirectAction.ResponseCode = route.RedirectAction_TEMPORARY_REDIRECT
	case 308:
		redirectAction.ResponseCode = route.RedirectAction_PERMANENT_REDIRECT
	default:
		redirectAction.ResponseCode = route.RedirectAction_TEMPORARY_REDIRECT
	}

	return &route.Route{
		Match: &route.RouteMatch{
			PathSpecifier: &route.RouteMatch_Prefix{
				Prefix: redirect.PathPrefix,
			},
		},
		Action: &route.Route_Redirect{
			Redirect: redirectAction,
		},
	}
}

// buildClusters creates Envoy clusters from backends
// https://www.envoyproxy.io/docs/envoy/latest/configuration/upstream/cluster_manager/cds
func (sg *SnapshotGenerator) buildClusters() []types.Resource {
	var resources []types.Resource

	for _, b := range sg.backends {
		c := &cluster.Cluster{
			Name:           fmt.Sprintf("cluster_%s", b.ID),
			ConnectTimeout: durationpb.New(5 * time.Second),
			ClusterDiscoveryType: &cluster.Cluster_Type{
				Type: cluster.Cluster_EDS,
			},
			EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
				ServiceName: fmt.Sprintf("cluster_%s", b.ID),
				EdsConfig: &core.ConfigSource{
					ResourceApiVersion: core.ApiVersion_V3,
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
				},
			},
			LbPolicy: cluster.Cluster_ROUND_ROBIN,
		}

		switch b.ResolverType {
		case StaticResolver:
			c.ClusterDiscoveryType = &cluster.Cluster_Type{
				Type: cluster.Cluster_EDS, // Internally, we will use EDS for static resolvers
			}
		case DnsResolver:
			c.ClusterDiscoveryType = &cluster.Cluster_Type{
				Type: cluster.Cluster_STRICT_DNS,
			}
			c.DnsLookupFamily = cluster.Cluster_V4_ONLY
			c.LbPolicy = cluster.Cluster_ROUND_ROBIN

			clusterEndpoints := make([]*endpoint.LbEndpoint, 0)

			if len(b.Hosts) > 0 {
				hostAddress := core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  b.Hosts[0],
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: uint32(b.Port),
					},
				}

				if b.DNSResolver != "" {
					hostAddress.ResolverName = b.DNSResolver
				}

				clusterEndpoint := endpoint.LbEndpoint{
					HostIdentifier: &endpoint.LbEndpoint_Endpoint{
						Endpoint: &endpoint.Endpoint{
							Address: &core.Address{
								Address: &core.Address_SocketAddress{
									SocketAddress: &hostAddress,
								},
							},
						},
					},
				}

				clusterEndpoints = append(clusterEndpoints, &clusterEndpoint)
			}

			c.LoadAssignment = &endpoint.ClusterLoadAssignment{
				ClusterName: fmt.Sprintf("cluster_%s", b.ID),
				Endpoints: []*endpoint.LocalityLbEndpoints{
					{
						LbEndpoints: clusterEndpoints,
					},
				},
			}
		}

		c.TransportSocket = sg.buildUpstreamTransportContext(&b)
		resources = append(resources, c)
	}

	return resources
}

// buildUpstreamTransportContext builds the transport socket for upstream connections with optional PROXY protocol and TLS
func (sg *SnapshotGenerator) buildUpstreamTransportContext(b *Backend) *core.TransportSocket {
	var innerTransportSocket *core.TransportSocket

	if b.IsTLS {
		tlsContext := sg.buildUpstreamTLSContext(b)
		innerTransportSocket = &core.TransportSocket{
			Name: "envoy.transport_sockets.tls",
			ConfigType: &core.TransportSocket_TypedConfig{
				TypedConfig: MustMarshalAny(tlsContext),
			},
		}
	} else {
		innerTransportSocket = &core.TransportSocket{
			Name: "envoy.transport_sockets.raw_buffer",
			ConfigType: &core.TransportSocket_TypedConfig{
				TypedConfig: MustMarshalAny(&rawbuffer.RawBuffer{}),
			},
		}
	}

	if b.ProxyProtocolVersion == ProxyProtocolNone {
		return innerTransportSocket
	}

	// Wrap Inner Transport Socket in Proxy Protocol wrapper
	envoyProxyProtocolVersion := core.ProxyProtocolConfig_V1
	if b.ProxyProtocolVersion == ProxyProtocolV2 {
		envoyProxyProtocolVersion = core.ProxyProtocolConfig_V2
	}
	proxyProtocolTransport := &proxyProtocolUpstream.ProxyProtocolUpstreamTransport{
		Config: &core.ProxyProtocolConfig{
			Version: envoyProxyProtocolVersion,
		},
		TransportSocket: innerTransportSocket,
	}

	return &core.TransportSocket{
		Name: "envoy.transport_sockets.upstream_proxy_protocol",
		ConfigType: &core.TransportSocket_TypedConfig{
			TypedConfig: MustMarshalAny(proxyProtocolTransport),
		},
	}
}

// buildUpstreamTLSContext creates TLS context for clusters
func (sg *SnapshotGenerator) buildUpstreamTLSContext(b *Backend) *tls.UpstreamTlsContext {
	tlsContext := &tls.UpstreamTlsContext{
		CommonTlsContext: &tls.CommonTlsContext{
			ValidationContextType: &tls.CommonTlsContext_ValidationContext{
				ValidationContext: &tls.CertificateValidationContext{},
			},
			TlsParams: &tls.TlsParameters{
				TlsMinimumProtocolVersion: tls.TlsParameters_TLSv1_2,
			},
		},
	}

	// If SNI domain has been specified, then,
	// Configure SNI / SAN matching for the upstream cluster
	if b.SNIDomain != "" {
		tlsContext.Sni = b.SNIDomain

		validationContext := tlsContext.CommonTlsContext.GetValidationContext()
		validationContext.MatchTypedSubjectAltNames = []*tls.SubjectAltNameMatcher{
			{
				SanType: tls.SubjectAltNameMatcher_DNS,
				Matcher: &matcher.StringMatcher{
					MatchPattern: &matcher.StringMatcher_Exact{Exact: b.SNIDomain},
				},
			},
		}
	}

	return tlsContext
}

// buildEndpoints creates Envoy endpoints from backends
// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/service_discovery
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/endpoint/v3/endpoint.proto#envoy-v3-api-msg-config-endpoint-v3-clusterloadassignment
func (sg *SnapshotGenerator) buildEndpoints() []types.Resource {
	var resources []types.Resource

	for _, b := range sg.backends {
		// Don't create endpoints for dns based resolvers, as they are handled by the cluster discovery
		if b.ResolverType == DnsResolver {
			continue
		}

		var lbEndpoints []*endpoint.LbEndpoint

		for _, host := range b.Hosts {
			lbEndpoints = append(lbEndpoints, &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Protocol: core.SocketAddress_TCP,
									Address:  host,
									PortSpecifier: &core.SocketAddress_PortValue{
										PortValue: uint32(b.Port),
									},
								},
							},
						},
					},
				},
			})
		}

		cla := &endpoint.ClusterLoadAssignment{
			ClusterName: fmt.Sprintf("cluster_%s", b.ID),
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: lbEndpoints,
				},
			},
		}

		resources = append(resources, cla)
	}

	return resources
}

// buildSecrets creates Envoy secrets from TLS certificates
// https://www.envoyproxy.io/docs/envoy/latest/configuration/security/secret
func (sg *SnapshotGenerator) buildSecrets() []types.Resource {
	var resources []types.Resource

	for _, cert := range sg.tlsCerts {
		secret := &tls.Secret{
			Name: fmt.Sprintf("tls_cert_%s", cert.ID),
			Type: &tls.Secret_TlsCertificate{
				TlsCertificate: &tls.TlsCertificate{
					CertificateChain: &core.DataSource{
						Specifier: &core.DataSource_InlineString{
							InlineString: cert.Cert,
						},
					},
					PrivateKey: &core.DataSource{
						Specifier: &core.DataSource_InlineString{
							InlineString: cert.Key,
						},
					},
				},
			},
		}

		resources = append(resources, secret)
	}

	return resources
}

// GetVersion returns the current snapshot version
func (sg *SnapshotGenerator) GetVersion() string {
	return sg.version
}
