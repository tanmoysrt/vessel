# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

import frappe
from frappe.model.document import Document

from captain.message_broker.doctype.nats_settings.nats_settings import get_nsc
from captain.utils.jobs import has_job_timeout_exceeded


class NATSUser(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		from captain.message_broker.doctype.nats_subject.nats_subject import NATSSubject

		account: DF.Data
		status: DF.Literal["Active", "Revoked", "Revocation Pending", "Revert Revocation Pending"]
		subjects: DF.Table[NATSSubject]
		user_id: DF.Data | None
	# end: auto-generated types

	@property
	def pub_subjects(self) -> list[str]:
		return [s.subject for s in self.subjects if s.type in ("Publish", "PubSub")]

	@property
	def sub_subjects(self) -> list[str]:
		return [s.subject for s in self.subjects if s.type in ("Subscribe", "PubSub")]

	def before_insert(self):
		if not frappe.db.exists("NATS Account", {"account_name": self.account}):
			frappe.throw(f"NATS Account {self.account} does not exist")

	def after_insert(self):
		nsc = get_nsc()
		self.user_id = nsc.add_user(self.account, self.name, pub=self.pub_subjects, sub=self.sub_subjects)
		self.db_update()

	def on_update(self):
		if self.flags.in_insert:
			return

		if self.has_value_changed("subjects"):
			# Subjects have changed, update user permissions
			nsc = get_nsc()
			nsc.update_user_permissions(self.account, self.name, pub=self.pub_subjects, sub=self.sub_subjects)

	def on_trash(self):
		nsc = get_nsc()
		nsc.delete_user(self.account, self.name, revoke=True)

	@frappe.whitelist()
	def get_credential(self) -> str:
		frappe.only_for("System Manager")
		if self.status != "Active":
			frappe.throw(f"User {self.name} is not active. Current status: {self.status}")

		nsc = get_nsc()
		return nsc.get_user_credential(self.account, self.name)

	@frappe.whitelist()
	def request_revocation(self):
		if self.status == "Revoked":
			frappe.throw("User is already revoked")

		if self.status == "Revocation Pending":
			frappe.throw("Revocation is already pending. Please wait.")

		self.status = "Revocation Pending"
		self.save()

		frappe.msgprint("Revocation request submitted. Please wait for the process to complete.")

	@frappe.whitelist()
	def revert_revocation(self):
		if self.status != "Revoked":
			frappe.throw(f"User should be revoked to revert revocation. Current status: {self.status}")

		self.status = "Revert Revocation Pending"
		self.save()

		frappe.msgprint("Revert revocation request submitted. Please wait for the process to complete.")


def process_revoke_requests():
	pending_users = frappe.get_all(
		"NATS User",
		filters={"status": "Revocation Pending"},
		fields=["name", "account"],
		limit_page_length=50,
	)
	accounts = set()
	nsc = get_nsc()
	for user in pending_users:
		try:
			nsc.revoke_user(user.account, user.name)
			frappe.db.set_value("NATS User", user.name, "status", "Revoked")
			accounts.add(user.account)
			frappe.db.commit()
		except Exception as e:
			frappe.log_error(f"Failed to revoke user {user.name}: {e}")
			frappe.db.rollback()

	# Trigger account sync for affected accounts
	for account in accounts:
		account_doc_name = frappe.db.exists("NATS Account", {"account_name": account})
		if not account_doc_name:
			continue

		if has_job_timeout_exceeded():
			continue

		# Take lock
		frappe.db.get_value("NATS Account", account_doc_name, "name", for_update=True)

		# Set pending_sync to True
		frappe.db.set_value("NATS Account", account_doc_name, "pending_sync", True)
		frappe.db.commit()


def process_revert_revocation_requests():
	pending_users = frappe.get_all(
		"NATS User",
		filters={"status": "Revert Revocation Pending"},
		fields=["name", "account"],
		limit_page_length=50,
	)
	accounts = set()
	nsc = get_nsc()
	for user in pending_users:
		try:
			nsc.remove_user_revocation(user.account, user.name)
			frappe.db.set_value("NATS User", user.name, "status", "Active")
			accounts.add(user.account)
			frappe.db.commit()
		except Exception as e:
			frappe.log_error(f"Failed to reinstate user {user.name}: {e}")
			frappe.db.rollback()

	# Trigger account sync for affected accounts
	for account in accounts:
		account_doc_name = frappe.db.exists("NATS Account", {"account_name": account})
		if not account_doc_name:
			continue

		if has_job_timeout_exceeded():
			continue

		# Take lock
		frappe.db.get_value("NATS Account", account_doc_name, "name", for_update=True)

		# Set pending_sync to True
		frappe.db.set_value("NATS Account", account_doc_name, "pending_sync", True)
		frappe.db.commit()


def trigger_process_revoke_requests():
	frappe.enqueue(
		"captain.message_broker.doctype.nats_user.nats_user.process_revoke_requests",
		queue="default",
		timeout=300,
		job_id="nats||process_revoke_requests",
		enqueue_after_commit=True,
	)


def trigger_process_revert_revocation_requests():
	frappe.enqueue(
		"captain.message_broker.doctype.nats_user.nats_user.process_revert_revocation_requests",
		queue="default",
		timeout=300,
		job_id="nats||process_revert_revocation_requests",
		enqueue_after_commit=True,
	)
