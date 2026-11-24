from contextlib import closing
import json
import math
from pathlib import Path
import re
import sqlite3
import sys


FIXTURE_ERROR = 10
OUTPUT_LIMIT = 11
MAX_FIXTURE_BYTES = 256 * 1024
MAX_VALUE_BYTES = 1024 * 1024
MAX_TABLES = 16
MAX_COLUMNS = 64
MAX_FIXTURE_ROWS = 10_000
MAX_RESULT_ROWS = 4096
IDENTIFIER = re.compile(r"[A-Za-z_][A-Za-z0-9_]{0,62}")
TYPES = {"INTEGER", "REAL", "TEXT"}
FUNCTIONS = frozenset("""
abs avg ceil ceiling char coalesce concat concat_ws count cume_dist date datetime
dense_rank first_value floor format glob group_concat hex ifnull instr julianday lag
last_value lead length like lower ltrim max min mod nth_value ntile nullif percent_rank
printf quote rank replace round row_number rtrim sign strftime string_agg substr substring
sum time timediff total trim typeof unicode unixepoch upper
""".split())


class OutputLimit(Exception):
    pass


def unique_object(items):
    obj = dict(items)
    if len(obj) != len(items):
        raise ValueError
    return obj


def identifier(value):
    if not isinstance(value, str) or not IDENTIFIER.fullmatch(value):
        raise ValueError
    return f'"{value}"'


def valid_cell(value, kind):
    if value is None:
        return True
    if kind == "TEXT":
        return isinstance(value, str)
    if type(value) is int:
        return -(1 << 63) <= value < (1 << 63)
    return kind == "REAL" and type(value) is float and math.isfinite(value)


def load_fixture(db, body):
    if len(body) > MAX_FIXTURE_BYTES:
        raise ValueError
    case = json.loads(body.decode("utf-8"), object_pairs_hook=unique_object)
    if not isinstance(case, dict) or case.keys() != {"ordered", "tables"}:
        raise ValueError
    if type(case["ordered"]) is not bool:
        raise ValueError
    tables = case["tables"]
    if not isinstance(tables, list) or len(tables) > MAX_TABLES:
        raise ValueError
    names = set()
    count = 0
    for table in tables:
        if not isinstance(table, dict) or table.keys() != {"name", "columns", "rows"}:
            raise ValueError
        name = identifier(table["name"])
        key = table["name"].lower()
        if key.startswith("sqlite_") or key in names:
            raise ValueError
        names.add(key)
        columns = table["columns"]
        if not isinstance(columns, list) or not 1 <= len(columns) <= MAX_COLUMNS:
            raise ValueError
        definitions, kinds, seen = [], [], set()
        for column in columns:
            if not isinstance(column, dict) or column.keys() != {"name", "type"}:
                raise ValueError
            col = identifier(column["name"])
            key = column["name"].lower()
            kind = column["type"]
            if key in seen or not isinstance(kind, str) or kind not in TYPES:
                raise ValueError
            seen.add(key)
            definitions.append(f"{col} {kind}")
            kinds.append(kind)
        rows = table["rows"]
        if not isinstance(rows, list) or count + len(rows) > MAX_FIXTURE_ROWS:
            raise ValueError
        count += len(rows)
        for row in rows:
            if not isinstance(row, list) or len(row) != len(kinds):
                raise ValueError
            if not all(valid_cell(value, kind) for value, kind in zip(row, kinds)):
                raise ValueError
        db.execute(f"CREATE TABLE {name} ({','.join(definitions)})")
        placeholders = ",".join("?" for _ in kinds)
        db.executemany(f"INSERT INTO {name} VALUES ({placeholders})", rows)
    return names, case["ordered"]


def authorizer(tables):
    def authorize(op, first, second, database, _context):
        if op in (sqlite3.SQLITE_SELECT, sqlite3.SQLITE_RECURSIVE):
            return sqlite3.SQLITE_OK
        if op == sqlite3.SQLITE_READ and first and first.lower() in tables:
            # SQLite reports a table-only read without a database for count(*).
            if database == "main" or (database is None and second == ""):
                return sqlite3.SQLITE_OK
        if op == sqlite3.SQLITE_FUNCTION and second and second.lower() in FUNCTIONS:
            return sqlite3.SQLITE_OK
        return sqlite3.SQLITE_DENY

    return authorize


def encode_rows(cursor, ordered, limit):
    rows = []
    size = 2
    for row in cursor:
        if len(rows) == MAX_RESULT_ROWS:
            raise OutputLimit
        data = json.dumps(row, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")
        size += len(data) + bool(rows)
        if size > limit:
            raise OutputLimit
        rows.append(data)
    if not ordered:
        rows.sort()
    return b"[" + b",".join(rows) + b"]"


def execute(query, body, limit):
    with closing(sqlite3.connect(":memory:", autocommit=True)) as db:
        try:
            db.setconfig(sqlite3.SQLITE_DBCONFIG_DEFENSIVE, True)
            db.setconfig(sqlite3.SQLITE_DBCONFIG_TRUSTED_SCHEMA, False)
            db.setconfig(sqlite3.SQLITE_DBCONFIG_DQS_DML, False)
            db.setlimit(sqlite3.SQLITE_LIMIT_ATTACHED, 0)
            db.setlimit(sqlite3.SQLITE_LIMIT_LENGTH, MAX_VALUE_BYTES)
            db.execute("PRAGMA temp_store=MEMORY")
            db.execute("BEGIN")
            tables, ordered = load_fixture(db, body)
            db.execute("COMMIT")
            db.execute("PRAGMA query_only=ON")
        except (sqlite3.Error, ValueError, OverflowError, RecursionError):
            return FIXTURE_ERROR, b""
        # The allowlist also blocks operations that query_only does not cover.
        db.set_authorizer(authorizer(tables))
        try:
            return 0, encode_rows(db.execute(query), ordered, limit)
        except OutputLimit:
            return OUTPUT_LIMIT, b""
        except sqlite3.Error as error:
            if getattr(error, "sqlite_errorcode", None) == sqlite3.SQLITE_TOOBIG:
                return OUTPUT_LIMIT, b""
            return 1, b""
        except (TypeError, ValueError):
            return 1, b""


def main():
    query = Path(sys.argv[1]).read_text(encoding="utf-8")
    body = sys.stdin.buffer.read(MAX_FIXTURE_BYTES + 1)
    code, output = execute(query, body, int(sys.argv[2]))
    if code == 0:
        sys.stdout.buffer.write(output)
    return code


if __name__ == "__main__":
    sys.exit(main())
