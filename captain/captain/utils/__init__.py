from datetime import datetime, timezone


def current_datetime_utc_rfc3339nano():
	dt = datetime.now(timezone.utc)
	# ensure full 9-digit precision like Go
	return dt.strftime("%Y-%m-%dT%H:%M:%S.") + f"{dt.microsecond:06d}000Z"
