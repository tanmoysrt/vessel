# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

from functools import cached_property

import frappe
import rq
from frappe.model.document import Document

from captain.message_broker.nsc import NSC
from captain.utils.jobs import has_job_timeout_exceeded


class NATSSettings(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		host: DF.Data
		is_nsc_initialized: DF.Check
		nsc_directory: DF.Data
		operator_account_id: DF.Data | None
		operator_id: DF.Data | None
		operator_user_id: DF.Data | None
		port: DF.Int
		system_operator: DF.Data
		system_user_id: DF.Data | None
	# end: auto-generated types

	@cached_property
	def nsc(self) -> "NSC":
		from captain.message_broker.nsc import NSC

		return NSC(self.nsc_directory, self.system_operator)

	@property
	def account_jwt_server_url(self) -> str:
		return f"nats://{self.host}:{self.port}"

	def validate(self):
		self.validate_host_and_port()
		self.validate_nsc_directory()
		self.validate_operator_name()

	def validate_host_and_port(self):
		if self.host == "":
			frappe.throw("Host cannot be empty")

		if self.port == 0:
			frappe.throw("Port cannot be 0")

	def validate_nsc_directory(self):
		import os

		if not self.nsc_directory:
			frappe.throw("NSC Directory must be set")

		if not os.path.exists(self.nsc_directory):
			frappe.throw(f"Provided NSC Directory {self.nsc_directory} does not exist")

	def validate_operator_name(self):
		if not self.system_operator:
			frappe.throw("System Operator Name cannot be empty")

		if " " in self.system_operator:
			frappe.throw("System Operator Name cannot contain spaces")

	def on_update(self):
		if (
			self.is_nsc_initialized
			and self.has_value_changed("system_operator")
			and self.get_value_before_save("system_operator") != ""
		):
			frappe.throw("Cannot change System Operator Name after NSC is initialized")

		if (
			self.is_nsc_initialized
			and (self.has_value_changed("host") or self.has_value_changed("port"))
			and (self.get_value_before_save("host") != "" or self.get_value_before_save("port") != 0)
		):
			try:
				self.nsc.set_account_jwt_server_url(self.account_jwt_server_url)
				frappe.msgprint(
					"Operator Config has been updated. Kindly update NATS Server configuration accordingly."
				)
			except Exception as e:
				frappe.throw(f"Failed to set Account JWT Server URL: {e}")

	# Initialization
	# --------------
	@frappe.whitelist()
	def init(self):
		if self.is_nsc_initialized:
			frappe.throw("NSC is already initialized")

		self.validate()

		if self.nsc.is_initialized():
			frappe.throw(
				f"NSC Directory {self.nsc_directory} is not empty. NSC seems to be already initialized."
			)

		try:
			self.nsc.init()
			# Add an entry in accounts table
			self.accounts = []
			self.append(
				"accounts",
			)
			frappe.get_doc(
				{
					"doctype": "NATS Account",
					"account_name": self.system_operator,
					"account_id": self.nsc.get_jwt_dict("account", self.system_operator).get("sub"),
					"revoked": False,
					"pending_sync": True,
				}
			).insert()
		except Exception as e:
			# Delete contents of NSC Directory in case of failure
			self.nsc.cleanup()
			# Throw the error to the user
			frappe.throw(f"Failed to initialize NSC: {e}")

		try:
			self.nsc.set_account_jwt_server_url(self.account_jwt_server_url)
		except Exception as e:
			frappe.throw(f"Failed to set Account JWT Server URL: {e}")

		self.is_nsc_initialized = True
		self.save()

		frappe.msgprint("NSC initialized successfully")
		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"sync_info",
			queue="default",
			timeout=300,
			job_id=f"nats||sync_info||{self.name}",
			enqueue_after_commit=True,
		)

	# Account Management
	# ------------------
	@frappe.whitelist()
	def add_account(self, account_name: str) -> bool:
		if not self.is_nsc_initialized:
			frappe.throw("NSC is not initialized")

		if account_name == self.system_operator:
			frappe.throw("Account name cannot be same as System Operator Name")

		self._lock_record()

		# Check if account already exists
		if frappe.db.exists("NATS Account", account_name):
			frappe.throw(f"Account {account_name} already exists")

		try:
			account_id = self.nsc.add_account(account_name, sync=False)
			frappe.get_doc(
				{
					"doctype": "NATS Account",
					"account_name": account_name,
					"account_id": account_id,
					"parent": self.name,
					"parenttype": self.doctype,
					"parentfield": "accounts",
					"revoked": False,
					"pending_sync": True,
				}
			).insert()
			frappe.msgprint(
				f"Account <a href='/app/nats-account/{account_name}'>{account_name}</a> added successfully"
			)
		except Exception as e:
			frappe.throw(f"Failed to add account {account_name}: {e}")

	# Info
	# -----
	@frappe.whitelist()
	def sync_info(self):
		if not self.is_nsc_initialized:
			return

		changed = False

		# Sync operator identifier
		operator_jwt_decoded = self.nsc.get_jwt_dict("operator")
		if self.operator_id != operator_jwt_decoded.get("sub"):
			self.operator_id = operator_jwt_decoded.get("sub")
			self.system_user_id = operator_jwt_decoded.get("nats", {}).get("system_account")
			changed = True

		# Sync operator account identifier
		operator_account_jwt_decoded = self.nsc.get_jwt_dict("account", self.system_operator)
		if self.operator_account_id != operator_account_jwt_decoded.get("sub"):
			self.operator_account_id = operator_account_jwt_decoded.get("sub")
			changed = True

		# Sync operator user identifier
		operator_user_jwt_decoded = self.nsc.get_jwt_dict("user", self.system_operator, self.system_operator)
		if self.operator_user_id != operator_user_jwt_decoded.get("sub"):
			self.operator_user_id = operator_user_jwt_decoded.get("sub")
			changed = True

		if changed:
			self.save()

	@frappe.whitelist()
	def show_nats_server_config(self):
		if not self.is_nsc_initialized:
			frappe.throw("NSC is not initialized")

		try:
			config = self.nsc.generate_nats_server_config()
		except Exception as e:
			frappe.throw(f"Failed to generate NATS server config: {e}")

		frappe.msgprint(f"<pre>{config}</pre>", title="NATS Server Configuration")

	# Internal / Utility
	# ------------------
	def _lock_record(self):
		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)


def get_nsc() -> "NSC":
	settings = frappe.get_value(
		"NATS Settings", None, ["is_nsc_initialized", "nsc_directory", "system_operator"], as_dict=True
	)
	if not settings or not settings.is_nsc_initialized:
		frappe.throw("NSC is not initialized")
	return NSC(
		nsc_directory=settings.nsc_directory,
		operator=settings.system_operator,
	)


def sync_accounts():
	accounts_with_pending_sync = frappe.get_all(
		"NATS Account", filters={"pending_sync": True}, pluck="name", limit=50
	)
	for account_doc_name in accounts_with_pending_sync:
		if has_job_timeout_exceeded():
			break
		try:
			"""
			There is no point in syncing parallelly
			As, we need to take a global lock on NSC directory during any changes
			For data integrity and avoiding race conditions
			"""
			frappe.get_doc("NATS Account", account_doc_name, for_update=True).sync()
			frappe.db.commit()
		except rq.timeouts.JobTimeoutException:
			frappe.db.rollback()
			return
		except Exception:
			frappe.log_error(f"Failed to sync account {account_doc_name}")
			frappe.db.rollback()


def trigger_sync_accounts():
	frappe.enqueue(
		"captain.message_broker.doctype.nats_settings.nats_settings.sync_accounts",
		queue="default",
		timeout=300,
		job_id="nats||sync_accounts",
		deduplicate=True,
	)
