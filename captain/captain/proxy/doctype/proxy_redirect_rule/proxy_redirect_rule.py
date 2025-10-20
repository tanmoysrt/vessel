# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
import uuid

import frappe
from frappe.model.document import Document

from captain.message_broker import publish_message
from captain.message_broker.doctype.nats_message.nats_message import NATSMessage
from captain.proxy.utils import ensure_state


class ProxyRedirectRule(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_state: DF.Data | None
		domain: DF.Data
		host_redirect: DF.Data | None
		is_tls: DF.Check
		last_request_id: DF.Data | None
		listener_bind_ip: DF.Data
		listener_port: DF.Int
		path_redirect: DF.Data | None
		previous_state: DF.Data
		priority: DF.Int
		redirect_status_code: DF.Int
		route_prefix: DF.Data
		scheme_redirect: DF.Literal["", "HTTP", "HTTPS"]
	# end: auto-generated types

	def validate(self):
		if not self.host_redirect and not self.path_redirect and not self.scheme_redirect:
			frappe.throw("At least one redirect option must be specified.")

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
		frappe.db.get_value("Proxy Redirect Rule", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Creating" if self.current_state == "Draft" else "Updating"
		self.desired_state = "Active"
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.http_redirect_rule.upsert",
			payload={
				"priority": self.priority,
				"bind_ip": self.listener_bind_ip,
				"port": self.listener_port,
				"is_tls": bool(self.is_tls),
				"domain": self.domain,
				"route_prefix": self.route_prefix,
				"scheme_redirect": self.scheme_redirect.lower(),
				"host_redirect": self.host_redirect,
				"path_redirect": self.path_redirect,
				"status_code": self.redirect_status_code,
			},
		)

	@frappe.whitelist()
	@ensure_state("Active")
	def delete(self):
		frappe.db.get_value("Proxy Redirect Rule", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Deleting"
		self.desired_state = ""
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.http_redirect_rule.delete",
			payload={
				"bind_ip": self.listener_bind_ip,
				"port": self.listener_port,
				"domain": self.domain,
				"route_prefix": self.route_prefix,
			},
		)


def process_upsert_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	redirect_rule_name = frappe.db.get_value(
		"Proxy Redirect Rule", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not redirect_rule_name:
		return

	redirect_rule: ProxyRedirectRule = frappe.get_doc("Proxy Redirect Rule", redirect_rule_name)
	if msg.payload_dict.get("success"):
		redirect_rule.previous_state = redirect_rule.current_state
		redirect_rule.current_state = redirect_rule.desired_state
	else:
		# TODO: in case of failure, sync the data from payload
		redirect_rule.current_state = redirect_rule.previous_state

	redirect_rule.flags.in_upsert_or_delete = True
	redirect_rule.save(ignore_version=True)


def process_delete_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	redirect_rule_name = frappe.db.get_value(
		"Proxy Redirect Rule", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not redirect_rule_name:
		return

	redirect_rule: ProxyRedirectRule = frappe.get_doc("Proxy Redirect Rule", redirect_rule_name)
	redirect_rule.flags.in_upsert_or_delete = True
	if msg.payload_dict.get("success"):
		frappe.delete_doc("Proxy Redirect Rule", redirect_rule.name)
	else:
		redirect_rule.current_state = redirect_rule.previous_state
		redirect_rule.save(ignore_version=True)
