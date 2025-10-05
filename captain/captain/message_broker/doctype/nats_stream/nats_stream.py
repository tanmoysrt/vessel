# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
from frappe.model.document import Document

from captain.message_broker.nats import NatsClient


class NATSStream(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		from captain.message_broker.doctype.nats_stream_subject.nats_stream_subject import NATSStreamSubject

		stream: DF.Data
		subjects: DF.Table[NATSStreamSubject]
	# end: auto-generated types

	def on_update(self):
		with NatsClient() as client:
			if self.flags.in_insert:
				client.create_stream(self.stream, [s.subject for s in self.subjects])
			else:
				client.update_stream(self.stream, [s.subject for s in self.subjects])

	def on_trash(self):
		with NatsClient() as client:
			client.delete_stream(self.stream)
