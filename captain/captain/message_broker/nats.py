import asyncio
import contextlib
import signal
from datetime import datetime

import frappe
import nats
import nest_asyncio
from nats.aio.msg import Msg
from nats.errors import ConnectionClosedError
from nats.errors import TimeoutError as NATSTimeoutError
from nats.js.api import DiscardPolicy, RetentionPolicy, StorageType, StreamConfig

from captain.message_broker.doctype.nats_message.nats_message import NATSMessage
from captain.message_broker.doctype.nats_settings.nats_settings import NATSSettings
from captain.message_broker.nsc import NSC


class NatsClient:
	_nest_asyncio_applied = False

	def __init__(self, user: str | None = None, account: str | None = None):
		settings = frappe.get_value(
			"NATS Settings", None, ["host", "port", "nsc_directory", "system_operator"], as_dict=True
		)
		self.url = f"nats://{settings.host}:{settings.port}"
		self.nsc = NSC(
			nsc_directory=settings.nsc_directory,
			operator=settings.system_operator,
		)
		self.system_operator = settings.system_operator
		self.user = user or self.system_operator
		self.account = account or self.system_operator
		self.nc: nats.NATS = None

		# Apply nest_asyncio only once
		if not NatsClient._nest_asyncio_applied:
			# Patch asyncio globally to allow nested event loops -- Multiple calls are redundant but safe
			nest_asyncio.apply()
			NatsClient._nest_asyncio_applied = True

		# Get or create event loop - don't create a new one if one exists
		try:
			self.loop = asyncio.get_running_loop()
		except RuntimeError:
			self.loop = asyncio.new_event_loop()
			asyncio.set_event_loop(self.loop)

	def _run_async(self, coro):
		"""Helper method to safely run async code"""
		return self.loop.run_until_complete(coro)

	def connect(self):
		async def _connect():
			self.nc = await nats.connect(
				self.url,
				user_credentials=self.nsc.get_user_credential_path(self.account, self.user),
			)
			await self.nc.flush()

		if self.loop and self.loop.is_closed():
			self.loop = asyncio.new_event_loop()
			asyncio.set_event_loop(self.loop)

		self._run_async(_connect())

	def close(self):
		async def _close():
			if self.nc:
				await self.nc.close()
				self.nc = None

		if self.nc:
			self._run_async(_close())

		# Only close loop if it's not running and we created it
		if not self.loop.is_running() and not self.loop.is_closed():
			self.loop.close()

	# Subscription / Publish handlers
	# ----------------------
	def subscribe(self, subject, cb):
		async def _subscribe():
			js = self.nc.jetstream(timeout=30)
			await js.subscribe(subject, cb=cb)
			await self.nc.subscribe(subject, cb=cb)

		self._run_async(_subscribe())

	def publish(self, subject, payload: bytes):
		async def _publish():
			js = self.nc.jetstream(timeout=30)
			data = await js.publish(subject, payload)
			return data

		return self._run_async(_publish())

	# Stream CRUD operations
	# ----------------------
	def create_stream(self, stream_name, subjects):
		async def _create_stream():
			js = self.nc.jetstream(timeout=30)
			await js.add_stream(
				config=StreamConfig(
					name=stream_name,
					subjects=subjects,
					retention=RetentionPolicy.WORK_QUEUE,
					discard=DiscardPolicy.OLD,
					storage=StorageType.FILE,
					max_msgs=-1,
					max_bytes=-1,
					deny_delete=True,
					allow_rollup_hdrs=False,
					deny_purge=False,
				)
			)

		self._run_async(_create_stream())

	def update_stream(self, stream_name, subjects):
		async def _update_stream():
			js = self.nc.jetstream(timeout=30)
			await js.update_stream(
				config=StreamConfig(
					name=stream_name,
					subjects=subjects,
					retention=RetentionPolicy.WORK_QUEUE,
					discard=DiscardPolicy.OLD,
					storage=StorageType.FILE,
					max_msgs=-1,
					max_bytes=-1,
					deny_delete=True,
					allow_rollup_hdrs=False,
					deny_purge=False,
				)
			)

		self._run_async(_update_stream())

	def delete_stream(self, stream_name):
		from nats.js.errors import NotFoundError

		async def _delete_stream():
			js = self.nc.jetstream(timeout=30)
			# If the stream does not exist, then silently ignore the error
			try:
				await js.delete_stream(stream_name)
			except NotFoundError:
				pass

		self._run_async(_delete_stream())

	def __enter__(self):
		self.connect()
		return self

	def __exit__(self, exc_type, exc_value, traceback):
		self.close()


class NATSBackgroundMessageProcessor:
	def __init__(self):
		self.nats_client: nats.NATS | None = None
		self.nats_settings: NATSSettings | None = None
		self.running = True
		self.settings_last_checked = 0
		self.settings_check_interval = 30  # Check settings every 30 seconds
		self.batch_size = 500
		self.active_subscriptions = set()
		self.site = frappe.local.site

	async def get_settings(self, force_refresh=False):
		current_time = asyncio.get_event_loop().time()

		if (
			force_refresh
			or self.nats_settings is None
			or (current_time - self.settings_last_checked) > self.settings_check_interval
		):
			if self.nats_settings:
				self.nats_settings.reload()
			else:
				self.nats_settings = frappe.get_single("NATS Settings")

			self.settings_last_checked = current_time

		return self.nats_settings

	def get_nats_settings_latest_modified_time(self) -> datetime:
		return frappe.get_single_value("NATS Settings", "modified")

	async def connect_nats(self):
		while self.running:
			try:
				settings = await self.get_settings(force_refresh=True)
				url = f"nats://{settings.host}:{settings.port}"
				nsc = NSC(
					nsc_directory=settings.nsc_directory,
					operator=settings.system_operator,
				)

				if self.nats_client and self.nats_client.is_connected:
					await self.nats_client.close()

				self.nats_client = nats.NATS()
				await self.nats_client.connect(
					servers=[url],
					user_credentials=nsc.get_user_credential_path(
						settings.system_operator, settings.system_operator
					),
					allow_reconnect=True,
					max_reconnect_attempts=-1,
					reconnect_time_wait=2,
				)
				await self.nats_client.flush()
				print(f"Connected to NATS at {url}")

				# Setup subscriptions after connection
				await self.setup_subscriptions()
				return True

			except Exception as e:
				print(f"Failed to connect to NATS: {e}. Retrying in 5 seconds...")
				if not self.running:
					return False
				await asyncio.sleep(5)

		return False

	async def message_handler(self, msg: Msg):
		"""Handle incoming NATS messages"""
		try:
			subject = msg.subject
			data = msg.data.decode()

			# Call the sync callback in a thread pool to avoid blocking
			loop = asyncio.get_event_loop()
			await loop.run_in_executor(None, self.process_incoming_message, subject, data)

			# Acknowledge the message
			await msg.ack()

		except Exception as e:
			print(f"Error handling message: {e}")
			import traceback

			traceback.print_exc()
			# Negative acknowledge on error so message can be redelivered
			with contextlib.suppress(Exception):
				await msg.nak()

	def process_incoming_message(self, subject: str, data: str):
		"""Sync callback for processing incoming messages"""
		try:
			# Initialize Frappe context for this thread
			frappe.init(site=self.site)
			frappe.connect()

			# Create incoming NATS message record
			doc: NATSMessage = frappe.get_doc(
				{
					"doctype": "NATS Message",
					"direction": "Incoming",
					"subject": subject,
					"payload": data,
					"received": 1,
				}
			)
			doc.insert(ignore_permissions=True)
			doc.trigger_message_handler()
			frappe.db.commit()

		except Exception as e:
			print(f"Error processing incoming message: {e}")
			import traceback

			traceback.print_exc()
		finally:
			# Clean up Frappe context
			with contextlib.suppress(Exception):
				frappe.destroy()

	async def setup_subscriptions(self):
		"""Setup durable push consumers based on settings"""
		try:
			settings = await self.get_settings()
			subjects = [s.subject for s in settings.consumer_subjects]

			if not subjects:
				return

			# Get JetStream context
			js = self.nats_client.jetstream()

			# Unsubscribe from subjects no longer in settings
			for subject in list(self.active_subscriptions):
				if subject not in subjects:
					self.active_subscriptions.remove(subject)

			# Subscribe to new subjects with durable consumers
			for subject in subjects:
				if subject not in self.active_subscriptions:
					stream_name = subject.split(".")[0]

					# Subscribe with durable consumer
					await js.subscribe(
						subject,
						cb=self.message_handler,
						durable=settings.consumer_name,
						stream=stream_name,
						manual_ack=True,
					)

					self.active_subscriptions.add(subject)
					print(f"Subscribed to subject: {subject} with durable consumer: {settings.consumer_name}")

		except Exception as e:
			print(f"Error setting up subscriptions: {e}")
			import traceback

			traceback.print_exc()

	async def publish_batch(self, messages: list):
		if not messages:
			return 0

		js = self.nats_client.jetstream()

		# Create publish tasks
		tasks = []
		for msg_name in messages:
			try:
				msg: NATSMessage = frappe.get_doc("NATS Message", msg_name)
				task = asyncio.create_task(js.publish(msg.subject, msg.payload.encode(), stream=msg.stream))
				tasks.append((msg_name, task))
			except Exception as e:
				print(f"Error loading message {msg_name}: {e}")

		# Wait for all publishes
		results = await asyncio.gather(*[task for _, task in tasks], return_exceptions=True)

		successful = 0
		for (msg_name, _), result in zip(tasks, results, strict=False):
			try:
				if isinstance(result, Exception):
					# Network errors - don't mark as failed, will be retried
					if isinstance(result, NATSTimeoutError | ConnectionClosedError | ConnectionError):
						continue

					import traceback

					trace = "".join(traceback.format_exception(type(result), result, result.__traceback__))
					frappe.db.set_value(
						"NATS Message", msg_name, {"failed": 1, "traceback": trace}, update_modified=False
					)
				else:
					frappe.db.set_value("NATS Message", msg_name, {"sent": 1}, update_modified=False)
					successful += 1
			except Exception as e:
				print(f"Error updating message {msg_name}: {e}")

		frappe.db.commit()
		return successful

	async def process_outgoing_messages(self):
		try:
			frappe.db.commit()

			messages = frappe.get_all(
				"NATS Message",
				filters={"direction": "Outgoing", "sent": 0, "failed": 0},
				limit=self.batch_size,
				pluck="name",
			)

			if messages:
				processed = await self.publish_batch(messages)
				if processed > 0:
					print(f"Published {processed}/{len(messages)} messages")

			return len(messages)

		except Exception as e:
			print(f"Error processing outgoing messages: {e}")
			import traceback

			traceback.print_exc()
			return 0

	async def run(self):
		"""Main processing loop"""
		# Setup signal handlers
		loop = asyncio.get_event_loop()

		def signal_handler():
			asyncio.create_task(self.shutdown())  # noqa: RUF006

		for sig in (signal.SIGTERM, signal.SIGINT):
			loop.add_signal_handler(sig, signal_handler)

		# Connect to NATS
		if not await self.connect_nats():
			return

		try:
			while self.running:
				# Check for settings changes and update subscriptions
				old_nats_settings_modified_time = self.nats_settings.modified if self.nats_settings else None

				await self.get_settings()
				if (
					old_nats_settings_modified_time
					and self.nats_settings.modified != old_nats_settings_modified_time
				):
					await self.setup_subscriptions()

				# Ensure connection
				if not self.nats_client or not self.nats_client.is_connected:
					if not await self.connect_nats():
						break

				# Process outgoing messages
				message_count = await self.process_outgoing_messages()

				# Sleep if no messages to avoid busy loop
				if message_count == 0:
					await asyncio.sleep(1)

		except Exception as e:
			print(f"Unexpected error: {e}")
			import traceback

			traceback.print_exc()
		finally:
			await self.shutdown()

	async def shutdown(self):
		print("\nShutting down gracefully...")
		self.running = False

		if self.nats_client and self.nats_client.is_connected:
			try:
				await self.nats_client.flush(timeout=5)
				await self.nats_client.close()
				print("NATS connection closed")
			except Exception as e:
				print(f"Error closing NATS connection: {e}")


def run_message_processor():
	processor = NATSBackgroundMessageProcessor()
	asyncio.run(processor.run())
