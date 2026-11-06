"""Task-owned MySQL/PostgreSQL fixtures for the opt-in shutdown workload.

No host ports or existing databases are used. The gateway creates its own schema;
this adapter only seeds synthetic rows and reads the resulting accounting state.
"""
import csv
import io
import math
import re
import time


class FixtureCursor:
    def __init__(self, rows):
        self.rows = rows
        self.position = 0

    def fetchone(self):
        if self.position == len(self.rows):
            return None
        row = self.rows[self.position]
        self.position += 1
        return row

    def __iter__(self):
        result = iter(self.rows[self.position:])
        self.position = len(self.rows)
        return result


class SQLFixture:
    def __init__(self, kind, prefix, network, resources, docker):
        self.kind = "postgres" if kind in ("pg", "postgresql", "postgres") else kind
        if self.kind not in ("mysql", "postgres"):
            raise ValueError("SQLFixture supports mysql or postgres")
        if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]*", prefix):
            raise ValueError("invalid fixture resource prefix")
        self.docker = docker
        self.container = prefix + "-" + self.kind
        command = ["run", "-d", "--name", self.container, "--label", "task=" + prefix, "--network", network]
        if self.kind == "mysql":
            command.extend(("-e", "MYSQL_ALLOW_EMPTY_PASSWORD=yes", "-e", "MYSQL_ROOT_HOST=%", "mysql:8.0", "--skip-log-bin", "--max-connections=300"))
        else:
            command.extend(("-e", "POSTGRES_HOST_AUTH_METHOD=trust", "postgres:17-alpine", "postgres", "-c", "max_connections=300"))
        docker(*command)
        resources.append(self.container)
        deadline = time.monotonic() + 90
        while True:
            try:
                self._execute("mysql" if self.kind == "mysql" else "postgres", "SELECT 1")
                break
            except RuntimeError:
                if time.monotonic() >= deadline:
                    raise RuntimeError(self.kind + " isolated fixture did not become ready") from None
                time.sleep(0.5)

    def create_database(self, name):
        self._validate_database(name)
        if self.kind == "mysql":
            statement = "CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
            self._execute("mysql", statement)
        else:
            self._execute("postgres", 'CREATE DATABASE "' + name + '"')

    def dsn(self, name):
        self._validate_database(name)
        if self.kind == "mysql":
            return "root@tcp(" + self.container + ":3306)/" + name + "?charset=utf8mb4&parseTime=True&loc=Local&timeout=5s&readTimeout=30s&writeTimeout=30s"
        return "postgres://postgres@" + self.container + ":5432/" + name + "?sslmode=disable&connect_timeout=5"

    def connect(self, name):
        self._validate_database(name)
        return SQLConnection(self, name)

    def _validate_database(self, name):
        if not re.fullmatch(r"[a-zA-Z][a-zA-Z0-9_]{0,62}", name):
            raise ValueError("invalid isolated fixture database name")

    def _execute(self, database, statement):
        if self.kind == "mysql":
            output = self.docker("exec", self.container, "mysql", "--protocol=TCP", "--host=127.0.0.1", "--user=root", "--default-character-set=utf8mb4", "--batch", "--raw", "--database=" + database, "--execute", statement)
            rows = [row.split("\t") for row in output.splitlines()]
        else:
            output = self.docker("exec", self.container, "psql", "-X", "--host=127.0.0.1", "--username=postgres", "--dbname=" + database, "--set=ON_ERROR_STOP=1", "--csv", "--pset=null=\\N", "--command", statement)
            rows = list(csv.reader(io.StringIO(output)))
        if not re.match(r"\s*(SELECT|WITH|SHOW)\b", statement, re.I) or not rows:
            return FixtureCursor([])
        columns, values = rows[0], rows[1:]
        parsed = []
        for row in values:
            if len(row) != len(columns):
                raise RuntimeError("unexpected " + self.kind + " fixture query result")
            parsed.append([
                None if value in ("\\N", "NULL") else
                int(value) if column != "request_id" and re.fullmatch(r"-?\d+", value) else value
                for column, value in zip(columns, row)
            ])
        return FixtureCursor(parsed)


class SQLConnection:
    def __init__(self, fixture, database):
        self.fixture = fixture
        self.database = database

    def execute(self, statement, arguments=()):
        statement = statement.strip().rstrip(";")
        replace_option = re.match(r"INSERT\s+OR\s+REPLACE\s+INTO\s+options\b", statement, re.I)
        if replace_option:
            statement = "INSERT INTO options" + statement[replace_option.end():]
            statement += (' ON CONFLICT ("key") DO UPDATE SET value=EXCLUDED.value' if self.fixture.kind == "postgres" else " ON DUPLICATE KEY UPDATE value=VALUES(value)")
        if self.fixture.kind == "mysql":
            # Source SQL uses double quotes only for identifiers. Values below
            # are encoded as hex literals, so quotes in values remain untouched.
            statement = re.sub(r'"([^"]+)"', r'`\1`', statement)
        parts = []
        position = 0
        quote = None
        index = 0
        while index < len(statement):
            char = statement[index]
            if quote:
                parts.append(char)
                if char == quote:
                    if index + 1 < len(statement) and statement[index + 1] == quote:
                        parts.append(statement[index + 1])
                        index += 1
                    else:
                        quote = None
            elif char in ("'", '"', "`"):
                quote = char
                parts.append(char)
            elif char == "?":
                if position == len(arguments):
                    raise ValueError("not enough fixture SQL arguments")
                value = arguments[position]
                position += 1
                if value is None:
                    parts.append("NULL")
                elif isinstance(value, bool):
                    parts.append("TRUE" if value else "FALSE")
                elif isinstance(value, int):
                    parts.append(str(value))
                elif isinstance(value, float) and math.isfinite(value):
                    parts.append(repr(value))
                elif isinstance(value, str):
                    encoded = value.encode("utf-8").hex()
                    parts.append("convert_from(decode('" + encoded + "','hex'),'UTF8')" if self.fixture.kind == "postgres" else "CONVERT(X'" + encoded + "' USING utf8mb4)")
                else:
                    raise ValueError("unsupported fixture SQL argument")
            else:
                parts.append(char)
            index += 1
        if position != len(arguments):
            raise ValueError("too many fixture SQL arguments")
        return self.fixture._execute(self.database, "".join(parts))

    def commit(self):
        pass  # Each CLI statement autocommits in its own connection.

    def close(self):
        pass
