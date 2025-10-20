# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

import frappe
from frappe.model.document import Document


class NATSMessage(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		direction: DF.Literal["Incoming", "Outgoing"]
		event: DF.Data | None
		failed: DF.Check
		payload: DF.Text
		processed: DF.Check
		sent: DF.Check
		stream: DF.Data | None
		subject: DF.Data
		traceback: DF.LongText | None
	# end: auto-generated types

	def before_insert(self):
		if not self.event or not self.stream:
			# Subject format should be -> <stream>.<agent_id>.<request/reply>.<event>
			parts = self.subject.split(".")
			if len(parts) >= 4:
				self.event = ".".join(parts[3:])
				self.stream = parts[0]
			else:
				self.stream = "unknown_stream"
				self.event = "unknown_event"

	def trigger_message_handler(self):
		if self.direction == "Outgoing" or not self.event or self.processed or self.failed:
			return

		if not self.find_message_handlers():
			return

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"call_msg_handler",
			queue="short",  # TODO: need custom queue for nats message callback handling
			job_id=f"nats_incoming_message|handler|{self.name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	def call_msg_handler(self):
		methods = self.find_message_handlers()
		if not methods:
			return

		for method in methods:
			try:
				frappe.get_attr(method)(self)
				self.db_set("processed", 1)
			except Exception as e:
				self.db_set("failed", 1)
				self.db_set("traceback", frappe.get_traceback())
				frappe.log_error(
					"NATS Message Handler Error",
					f"Error in NATS message handler {method} for message {self.name}: {e!s}",
				)
				return

	def find_message_handlers(self):
		callback_methods = frappe.get_hooks("nats_incoming_message_handlers") or {}
		return callback_methods.get(self.event, None)
