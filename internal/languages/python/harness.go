package python

import "overmindv/sandbox/internal/execution"

func HarnessFile() execution.ExecutionFile {
	return execution.ExecutionFile{
		Path:    HarnessPluginPath,
		Kind:    execution.FileKindHarness,
		Content: []byte(HarnessSource()),
	}
}

func HarnessSource() string {
	return `import json
import os
import time


REPORT_PATH = os.environ.get("SANDBOX_PYTEST_REPORT", ".sandbox/pytest-report.json")
_TESTS = {}


def _duration_ms(report):
    return int(round(float(getattr(report, "duration", 0.0)) * 1000))


def _message(longrepr):
    if longrepr is None:
        return ""
    reprcrash = getattr(longrepr, "reprcrash", None)
    if reprcrash is not None and getattr(reprcrash, "message", ""):
        return reprcrash.message
    return str(longrepr)


def _store(name, status, duration_ms=0, message=""):
    current = _TESTS.get(name)
    if current is not None and current.get("status") == "error":
        return
    _TESTS[name] = {
        "name": name,
        "status": status,
        "duration_ms": duration_ms,
        "message": message or "",
    }


def pytest_runtest_logreport(report):
    name = getattr(report, "nodeid", "unknown")
    if report.when == "call":
        if report.passed:
            status = "passed"
        elif report.failed:
            status = "failed"
        elif report.skipped:
            status = "skipped"
        else:
            status = "unknown"
        _store(name, status, _duration_ms(report), _message(getattr(report, "longrepr", None)))
        return

    if report.when in ("setup", "teardown") and report.failed:
        _store(name, "error", _duration_ms(report), _message(getattr(report, "longrepr", None)))


def pytest_collectreport(report):
    if report.failed:
        name = getattr(report, "nodeid", "") or "collection"
        _store(name, "error", _duration_ms(report), _message(getattr(report, "longrepr", None)))


def pytest_sessionfinish(session, exitstatus):
    directory = os.path.dirname(REPORT_PATH)
    if directory:
        os.makedirs(directory, exist_ok=True)
    payload = {
        "schema_version": "sandbox.pytest.report.v1",
        "exitstatus": int(exitstatus),
        "generated_at": int(time.time()),
        "tests": list(_TESTS.values()),
    }
    with open(REPORT_PATH, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, separators=(",", ":"), sort_keys=True)
`
}
