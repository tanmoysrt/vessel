import asyncio

import frappe
import nats
import nest_asyncio

from captain.message_broker.nsc import NSC


class NatsClient:
	_nest_asyncio_applied = False

	def __init__(self):
		settings = frappe.get_value(
			"NATS Settings", None, ["host", "port", "nsc_directory", "system_operator"], as_dict=True
		)
		self.url = f"nats://{settings.host}:{settings.port}"
		self.nsc = NSC(
			nsc_directory=settings.nsc_directory,
			operator=settings.system_operator,
		)
		self.system_operator = settings.system_operator
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
				user_credentials=self.nsc.get_user_credential_path(
					self.system_operator, self.system_operator
				),
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

	def create_stream(self, stream_name, subjects):
		async def _create_stream():
			js = self.nc.jetstream(timeout=30)
			await js.add_stream(name=stream_name, subjects=subjects)

		self._run_async(_create_stream())

	def update_stream(self, stream_name, subjects):
		async def _update_stream():
			js = self.nc.jetstream(timeout=30)
			await js.update_stream(name=stream_name, subjects=subjects)

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
