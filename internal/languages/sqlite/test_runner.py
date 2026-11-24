import json
import unittest

from runner import execute, FIXTURE_ERROR, OUTPUT_LIMIT


def fixture(ordered=False):
    return {
        "ordered": ordered,
        "tables": [
            {"name": "people", "columns": [{"name": "id", "type": "INTEGER"}, {"name": "name", "type": "TEXT"}],
             "rows": [[1, "Ada"], [2, "Lin"]]},
            {"name": "scores", "columns": [{"name": "id", "type": "INTEGER"}, {"name": "score", "type": "REAL"}],
             "rows": [[1, 3.5], [2, None], [1, 3.5]]},
        ],
    }


def run(query, case=None, limit=1024):
    return execute(query, json.dumps(fixture() if case is None else case).encode(), limit)


class SQLiteRunnerTest(unittest.TestCase):
    def test_join_sorts_rows_and_keeps_duplicates(self):
        query = "SELECT name, score FROM people JOIN scores USING (id) ORDER BY name DESC"
        self.assertEqual(run(query), (0, b'[["Ada",3.5],["Ada",3.5],["Lin",null]]'))
        self.assertNotEqual(run("SELECT DISTINCT name, score FROM people JOIN scores USING (id)")[1], run(query)[1])

    def test_ordered_case_preserves_query_order(self):
        self.assertEqual(
            run("SELECT name FROM people ORDER BY name DESC", fixture(ordered=True)),
            (0, b'[["Lin"],["Ada"]]'),
        )

    def test_fixture_values_are_bound_as_data(self):
        case = fixture()
        text = 'A "name"; (with punctuation)'
        case["tables"][0]["rows"] = [[1, text]]
        self.assertEqual(json.loads(run("SELECT name FROM people", case)[1]), [[text]])
        self.assertEqual(run("SELECT count(*) FROM people"), (0, b"[[2]]"))

    def test_disallows_non_query_actions_and_multiple_statements(self):
        for sql in (
            "UPDATE people SET name = 'changed'",
            "CREATE TABLE extra (id INTEGER)",
            "ATTACH DATABASE ':memory:' AS another",
            "PRAGMA query_only",
            "SELECT name FROM sqlite_schema",
            "SELECT name FROM pragma_table_info('people')",
            "SELECT 1; SELECT 2",
        ):
            with self.subTest(sql=sql):
                self.assertEqual(run(sql), (1, b""))

    def test_invalid_fixture_is_not_a_user_query_error(self):
        case = fixture()
        case["tables"][0]["rows"][0][0] = "invalid integer"
        self.assertEqual(run("SELECT name FROM people", case), (FIXTURE_ERROR, b""))
        case = fixture()
        case["tables"][0]["name"] = "not a table identifier"
        self.assertEqual(run("SELECT 1", case), (FIXTURE_ERROR, b""))
        self.assertEqual(execute("SELECT 1", b'{"ordered":true,"ordered":false,"tables":[]}', 12),
                         (FIXTURE_ERROR, b""))

    def test_enforces_output_budget_and_row_limit(self):
        self.assertEqual(run("SELECT 1", limit=5), (0, b"[[1]]"))
        self.assertEqual(run("SELECT 1", limit=4), (OUTPUT_LIMIT, b""))
        case = fixture()
        case["tables"][0]["rows"] = [[1, "a"]] * 4097
        self.assertEqual(run("SELECT id FROM people", case, limit=262144), (OUTPUT_LIMIT, b""))


if __name__ == "__main__":
    unittest.main()
