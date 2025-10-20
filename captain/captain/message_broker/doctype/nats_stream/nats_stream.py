# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

import frappe
from frappe.model.document import Document

from captain.message_broker.nats import NatsClient


class NATSStream(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from captain.message_broker.doctype.nats_stream_subject.nats_stream_subject import NATSStreamSubject
		from frappe.types import DF

		account: DF.Link
		stream: DF.Data
		subjects: DF.Table[NATSStreamSubject]
	# end: auto-generated types

	def before_insert(self):
		# Don't allow . * or > in stream names
		if any(char in self.stream for char in [".", "*", ">"]):
			frappe.throw("Stream name cannot contain '.', '*' or '>' characters.")

		# Validate subjects
		self.validate_subjects()

		# Check for stream uniqueness
		if frappe.db.exists("NATS Stream", {"account": self.account, "stream": self.stream}):
			frappe.throw(f"Stream {self.stream} already exists for account {self.account}.")

	def on_update(self):
		self.validate_subjects()

		with NatsClient(user=self.account, account=self.account) as client:
			if self.flags.in_insert:
				client.create_stream(self.stream, [s.subject for s in self.subjects])
			else:
				client.update_stream(self.stream, [s.subject for s in self.subjects])

	def validate_subjects(self):
		# Ensure all subjects start with the stream name
		unique_subjects = set()
		for subject in self.subjects:
			if not subject.subject.startswith(self.stream + "."):
				frappe.throw(f"Subject {subject.subject} must start with the stream name {self.stream}.")
			unique_subjects.add(subject.subject)

		if len(unique_subjects) != len(self.subjects):
			frappe.throw("Duplicate subjects found.")

	def on_trash(self):
		with NatsClient(user=self.account, account=self.account) as client:
			client.delete_stream(self.stream)
