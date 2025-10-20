# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

import uuid

import frappe
from frappe.model.document import Document

from captain.message_broker import publish_message
from captain.message_broker.doctype.nats_message.nats_message import NATSMessage
from captain.proxy.utils import ensure_state


class ProxyIngressRule(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		from captain.proxy.doctype.proxy_ip_address.proxy_ip_address import ProxyIPAddress

		allowed_ip_addresses: DF.Table[ProxyIPAddress]
		backend_dns_resolver: DF.Data
		backend_host_name: DF.Data
		backend_ip_addresses: DF.Table[ProxyIPAddress]
		backend_is_tls: DF.Check
		backend_port: DF.Int
		backend_resolver: DF.Literal["Static", "DNS"]
		backend_sni_domain: DF.Data
		blocked_ip_addresses: DF.Table[ProxyIPAddress]
		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_state: DF.Data | None
		domain: DF.Data
		is_tls: DF.Check
		last_request_id: DF.Data | None
		listener_bind_ip: DF.Data
		listener_port: DF.Int
		previous_state: DF.Data
		priority: DF.Int
		protocol: DF.Literal["HTTP", "TCP"]
		route_prefix: DF.Data
	# end: auto-generated types

	def after_insert(self):
		if not self.desired_state:
			self.desired_state = "Active"

		self.upsert()

	def on_update(self):
		if self.is_new():
			return

		if self.flags.in_upsert_or_delete:
			return

		self.upsert()

	@frappe.whitelist()
	@ensure_state("Draft", "Active")
	def upsert(self):
		frappe.db.get_value("Proxy Ingress Rule", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Creating" if self.current_state == "Draft" else "Updating"
		self.desired_state = "Active"
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.ingress_rule.upsert",
			payload={
				"priority": self.priority,
				"bind_ip": self.listener_bind_ip,
				"port": self.listener_port,
				"protocol": self.protocol.lower(),
				"is_tls": bool(self.is_tls),
				"domain": self.domain,
				"route_prefix": self.route_prefix,
				"allowed_cidrs": [],
				"denied_cidrs": [],
				"backend_resolver": self.backend_resolver.lower(),
				"backend_dns_resolver": self.backend_dns_resolver,
				"backend_hosts": [self.backend_host_name]
				if self.backend_resolver == "DNS"
				else [ip.address for ip in self.backend_ip_addresses],
				"backend_port": self.backend_port,
				"backend_is_tls": bool(self.backend_is_tls),
				"backend_sni_domain": self.backend_sni_domain,
			},
		)

	@frappe.whitelist()
	@ensure_state("Active")
	def delete(self):
		frappe.db.get_value("Proxy Ingress Rule", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Deleting"
		self.desired_state = ""
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.ingress_rule.delete",
			payload={
				"bind_ip": self.listener_bind_ip,
				"port": self.listener_port,
				"protocol": self.protocol,
				"domain": self.domain,
				"route_prefix": self.route_prefix,
			},
		)


def process_upsert_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	ingress_rule_name = frappe.db.get_value(
		"Proxy Ingress Rule", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not ingress_rule_name:
		return

	ingress_rule: ProxyIngressRule = frappe.get_doc("Proxy Ingress Rule", ingress_rule_name)
	if msg.payload_dict.get("success"):
		ingress_rule.previous_state = ingress_rule.current_state
		ingress_rule.current_state = ingress_rule.desired_state
	else:
		# TODO: in case of failure, sync the data from payload
		ingress_rule.current_state = ingress_rule.previous_state

	ingress_rule.flags.in_upsert_or_delete = True
	ingress_rule.save(ignore_version=True)


def process_delete_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	ingress_rule_name = frappe.db.get_value(
		"Proxy Ingress Rule", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not ingress_rule_name:
		return

	ingress_rule: ProxyIngressRule = frappe.get_doc("Proxy Ingress Rule", ingress_rule_name)
	ingress_rule.flags.in_upsert_or_delete = True
	if msg.payload_dict.get("success"):
		frappe.delete_doc("Proxy Ingress Rule", ingress_rule.name)
	else:
		ingress_rule.current_state = ingress_rule.previous_state
		ingress_rule.save(ignore_version=True)
