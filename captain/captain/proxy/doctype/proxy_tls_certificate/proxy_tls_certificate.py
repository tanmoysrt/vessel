# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
import uuid

import frappe
from frappe.model.document import Document

from captain.message_broker import publish_message
from captain.message_broker.doctype.nats_message.nats_message import NATSMessage
from captain.proxy.utils import ensure_state, validate_cert_and_key_pem


class ProxyTLSCertificate(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		certificate_pem: DF.LongText
		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_state: DF.Data | None
		domain: DF.Data
		expiry_time: DF.Datetime | None
		is_wildcard: DF.Check
		last_request_id: DF.Data | None
		previous_state: DF.Data | None
		private_key_pem: DF.LongText
	# end: auto-generated types

	def autoname(self):
		if self.is_wildcard:
			self.name = f"*.{self.domain}"
		else:
			self.name = self.domain

	def after_insert(self):
		if not self.desired_state:
			self.desired_state = "Active"

		self.upsert()

	def on_update(self):
		if self.is_new():
			self.validate_certificate_and_key()
			return

		if self.flags.in_upsert_or_delete:
			return

		if self.has_value_changed("certificate_pem") or self.has_value_changed("private_key_pem"):
			self.validate_certificate_and_key()

		self.upsert()

	def validate_certificate_and_key(self):
		if not self.certificate_pem.endswith("\n"):
			self.certificate_pem += "\n"
		if not self.private_key_pem.endswith("\n"):
			self.private_key_pem += "\n"

		is_valid, expiry_time_utc = validate_cert_and_key_pem(self.certificate_pem, self.private_key_pem)
		if not is_valid:
			frappe.throw("The provided certificate PEM and private key PEM are not valid or do not match.")
		self.expiry_time = expiry_time_utc.replace(tzinfo=None)
		self.db_update()

	@frappe.whitelist()
	@ensure_state("Draft", "Active")
	def upsert(self):
		frappe.db.get_value("Proxy TLS Certificate", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Creating" if self.current_state == "Draft" else "Updating"
		self.desired_state = "Active"
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.tls_certificate.upsert",
			payload={
				"domain": self.domain,
				"is_wildcard": bool(self.is_wildcard),
				"cert": self.certificate_pem,
				"key": self.private_key_pem,
			},
		)

	@frappe.whitelist()
	@ensure_state("Active")
	def delete(self):
		frappe.db.get_value("Proxy TLS Certificate", self.name, "name", for_update=True)

		self.flags.in_upsert_or_delete = True
		self.previous_state = self.current_state
		self.current_state = "Deleting"
		self.desired_state = ""
		self.last_request_id = str(uuid.uuid4())
		self.save(ignore_version=True)

		publish_message(
			request_id=self.last_request_id,
			subject="proxy.node1.request.v1.tls_certificate.delete",
			payload={
				"domain": self.domain,
				"is_wildcard": bool(self.is_wildcard),
			},
		)


def process_upsert_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	tls_certificate_name = frappe.db.get_value(
		"Proxy TLS Certificate", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not tls_certificate_name:
		return

	tls_certificate: ProxyTLSCertificate = frappe.get_doc("Proxy TLS Certificate", tls_certificate_name)
	if msg.payload_dict.get("success"):
		tls_certificate.previous_state = tls_certificate.current_state
		tls_certificate.current_state = tls_certificate.desired_state
	else:
		# TODO: in case of failure, sync the data from payload
		tls_certificate.current_state = tls_certificate.previous_state

	tls_certificate.flags.in_upsert_or_delete = True
	tls_certificate.save(ignore_version=True)


def process_delete_response(msg: NATSMessage):
	if "request_id" not in msg.payload_dict:
		return

	tls_certificate_name = frappe.db.get_value(
		"Proxy TLS Certificate", {"last_request_id": msg.payload_dict["request_id"]}, "name"
	)
	if not tls_certificate_name:
		return

	tls_certificate: ProxyTLSCertificate = frappe.get_doc("Proxy TLS Certificate", tls_certificate_name)
	tls_certificate.flags.in_upsert_or_delete = True
	if msg.payload_dict.get("success"):
		frappe.delete_doc("Proxy TLS Certificate", tls_certificate.name)
	else:
		tls_certificate.current_state = tls_certificate.previous_state
		tls_certificate.save(ignore_version=True)
