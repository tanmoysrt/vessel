# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
from frappe.model.document import Document


class ProxyIngressRule(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		from captain.proxy.doctype.proxy_ip_address.proxy_ip_address import ProxyIPAddress

		allowed_ip_addresses: DF.Table[ProxyIPAddress]
		backend_dns_resolver: DF.Data | None
		backend_ip_addresses: DF.Table[ProxyIPAddress]
		backend_is_tls: DF.Check
		backend_port: DF.Int
		backend_resolver: DF.Literal["Static", "DNS"]
		backend_sni_domain: DF.Data | None
		bakend_host_name: DF.Data | None
		blocked_ip_addresses: DF.Table[ProxyIPAddress]
		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_state: DF.Data | None
		domain: DF.Data | None
		is_tls: DF.Check
		listener_bind_ip: DF.Data
		listener_port: DF.Int
		previous_state: DF.Data
		priority: DF.Int
		protocol: DF.Literal["HTTP", "TCP"]
		route_prefix: DF.Data | None
	# end: auto-generated types

	pass
