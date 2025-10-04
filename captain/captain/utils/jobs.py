import signal

from rq import get_current_job


def has_job_timeout_exceeded() -> bool:
	# RQ sets up an alarm signal and a signal handler that raises
	# JobTimeoutException after the timeout amount
	# getitimer returns the time left for this timer
	# 0.0 means the timer is expired
	return bool(get_current_job()) and (signal.getitimer(signal.ITIMER_REAL)[0] <= 0)
