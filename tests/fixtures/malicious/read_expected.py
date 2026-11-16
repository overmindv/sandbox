from pathlib import Path
for name in ["expected.txt", "suite.json", ".sandbox/expected.json"]:
    if Path(name).exists():
        raise RuntimeError("expected data exposed")
print("expected unavailable")
