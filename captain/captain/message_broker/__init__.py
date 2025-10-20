import json
import uuid
from typing import TYPE_CHECKING

import frappe

from captain.utils import current_datetime_utc_rfc3339nano

from .nats import NATSBackgroundMessageProcessor, NatsClient, run_message_processor
from .nsc import NSC

if TYPE_CHECKING:
	from captain.message_broker.doctype.nats_message.nats_message import NATSMessage


def publish_message(subject: str, payload: dict, request_id: str | None = None) -> "NATSMessage":
	if "request_id" not in payload:
		payload["request_id"] = str(uuid.uuid4()) if not request_id else request_id
	if "requested_at" not in payload:
		payload["requested_at"] = current_datetime_utc_rfc3339nano()

	payload = json.dumps(payload, indent=" ").strip()

	return frappe.get_doc(
		{
			"doctype": "NATS Message",
			"direction": "Outgoing",
			"subject": subject,
			"payload": payload,
		}
	).insert(ignore_permissions=True)


__all__ = ["NSC", "NATSBackgroundMessageProcessor", "NatsClient", "publish_message", "run_message_processor"]
