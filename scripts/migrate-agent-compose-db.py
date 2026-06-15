#!/usr/bin/env python3
"""Migrate agent-compose SQLite database schema names.

This script renames old prefixed control-plane tables and indexes to the
unprefixed schema names used by current agent-compose builds. It is intentionally
standalone; the server does not perform this compatibility migration at startup.
"""

from __future__ import annotations

import argparse
import shutil
import sqlite3
import sys
from pathlib import Path


TABLE_RENAMES = (
    ("adp" + "_event", "event"),
    ("adp" + "lite_global_env", "global_env"),
    ("adp" + "lite_workspace_config", "workspace_config"),
    ("adp" + "lite_agent_definition", "agent_definition"),
    ("adp" + "lite_loader", "loader"),
    ("adp" + "lite_loader_trigger", "loader_trigger"),
    ("adp" + "lite_loader_run", "loader_run"),
    ("adp" + "lite_loader_event", "loader_event"),
    ("adp" + "lite_loader_state", "loader_state"),
    ("adp" + "lite_loader_binding", "loader_binding"),
)

INDEX_DROPS = (
    "idx_" + "adp" + "_event_correlation",
    "idx_" + "adp" + "_event_topic_sequence",
    "idx_" + "adp" + "_event_dispatch",
    "idx_" + "adp" + "_event_idempotency",
    "idx_" + "adp" + "lite_agent_definition_deleted_enabled",
    "idx_" + "adp" + "lite_agent_definition_workspace",
    "idx_" + "adp" + "lite_loader_trigger_schedule",
    "idx_" + "adp" + "lite_loader_run_started",
    "idx_" + "adp" + "lite_loader_event_created",
)

FOREIGN_KEY_REBUILDS = {
    "loader_trigger": (
        """CREATE TABLE loader_trigger__agent_compose_migration (
            loader_id TEXT NOT NULL,
            trigger_id TEXT NOT NULL,
            kind TEXT NOT NULL,
            topic TEXT NOT NULL DEFAULT '',
            interval_ms INTEGER NOT NULL DEFAULT 0,
            enabled INTEGER NOT NULL DEFAULT 1,
            auto_id INTEGER NOT NULL DEFAULT 0,
            spec_json TEXT NOT NULL DEFAULT '{}',
            next_fire_at INTEGER NOT NULL DEFAULT 0,
            last_fired_at INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY(loader_id, trigger_id),
            FOREIGN KEY(loader_id) REFERENCES loader(id) ON DELETE CASCADE
        )""",
        "loader_trigger__agent_compose_migration",
    ),
    "loader_run": (
        """CREATE TABLE loader_run__agent_compose_migration (
            loader_id TEXT NOT NULL,
            run_id TEXT NOT NULL,
            trigger_id TEXT NOT NULL DEFAULT '',
            trigger_kind TEXT NOT NULL DEFAULT '',
            trigger_source TEXT NOT NULL DEFAULT '',
            status TEXT NOT NULL DEFAULT '',
            started_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
            completed_at INTEGER NOT NULL DEFAULT 0,
            duration_ms INTEGER NOT NULL DEFAULT 0,
            error TEXT NOT NULL DEFAULT '',
            result_json TEXT NOT NULL DEFAULT '',
            payload_json TEXT NOT NULL DEFAULT '',
            source_script_sha256 TEXT NOT NULL DEFAULT '',
            artifacts_dir TEXT NOT NULL DEFAULT '',
            PRIMARY KEY(loader_id, run_id),
            FOREIGN KEY(loader_id) REFERENCES loader(id) ON DELETE CASCADE
        )""",
        "loader_run__agent_compose_migration",
    ),
    "loader_event": (
        """CREATE TABLE loader_event__agent_compose_migration (
            loader_id TEXT NOT NULL,
            event_id TEXT NOT NULL,
            run_id TEXT NOT NULL DEFAULT '',
            trigger_id TEXT NOT NULL DEFAULT '',
            type TEXT NOT NULL,
            level TEXT NOT NULL DEFAULT 'info',
            message TEXT NOT NULL DEFAULT '',
            payload_json TEXT NOT NULL DEFAULT '',
            linked_session_id TEXT NOT NULL DEFAULT '',
            linked_cell_id TEXT NOT NULL DEFAULT '',
            linked_agent_session_id TEXT NOT NULL DEFAULT '',
            created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
            PRIMARY KEY(loader_id, event_id),
            FOREIGN KEY(loader_id) REFERENCES loader(id) ON DELETE CASCADE
        )""",
        "loader_event__agent_compose_migration",
    ),
    "loader_state": (
        """CREATE TABLE loader_state__agent_compose_migration (
            loader_id TEXT NOT NULL,
            key TEXT NOT NULL,
            value_json TEXT NOT NULL,
            updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
            PRIMARY KEY(loader_id, key),
            FOREIGN KEY(loader_id) REFERENCES loader(id) ON DELETE CASCADE
        )""",
        "loader_state__agent_compose_migration",
    ),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Rename legacy agent-compose SQLite schema tables to unprefixed names.",
    )
    parser.add_argument("db_path", type=Path, help="Path to data.db")
    parser.add_argument(
        "--no-backup",
        action="store_true",
        help="Do not create a .bak copy before modifying the database.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print planned changes without modifying the database.",
    )
    return parser.parse_args()


def object_exists(conn: sqlite3.Connection, object_type: str, name: str) -> bool:
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type = ? AND name = ?",
        (object_type, name),
    ).fetchone()
    return row is not None


def quote_ident(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def table_row_count(conn: sqlite3.Connection, table_name: str) -> int:
    row = conn.execute(f"SELECT COUNT(*) FROM {quote_ident(table_name)}").fetchone()
    return int(row[0])


def table_columns(conn: sqlite3.Connection, table_name: str) -> list[str]:
    rows = conn.execute(f"PRAGMA table_info({quote_ident(table_name)})").fetchall()
    return [str(row[1]) for row in rows]


def copy_table_sql(conn: sqlite3.Connection, old_name: str, new_name: str) -> str:
    old_columns = table_columns(conn, old_name)
    new_columns = table_columns(conn, new_name)
    columns = [name for name in new_columns if name in old_columns]
    if not columns:
        raise RuntimeError(f"cannot copy {old_name}: no shared columns with {new_name}")
    column_list = ", ".join(quote_ident(name) for name in columns)
    return (
        f"INSERT INTO {quote_ident(new_name)} ({column_list}) "
        f"SELECT {column_list} FROM {quote_ident(old_name)}"
    )


def copy_columns_sql(columns: list[str], old_name: str, new_name: str) -> str:
    if not columns:
        raise RuntimeError(f"cannot copy {old_name}: no columns found")
    column_list = ", ".join(quote_ident(name) for name in columns)
    return (
        f"INSERT INTO {quote_ident(new_name)} ({column_list}) "
        f"SELECT {column_list} FROM {quote_ident(old_name)}"
    )


def copy_all_existing_columns_sql(conn: sqlite3.Connection, old_name: str, new_name: str) -> str:
    return copy_columns_sql(table_columns(conn, old_name), old_name, new_name)


def table_sql_contains(conn: sqlite3.Connection, table_name: str, pattern: str) -> bool:
    row = conn.execute(
        "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
        (table_name,),
    ).fetchone()
    return row is not None and pattern in str(row[0])


def rebuild_table_plan(conn: sqlite3.Connection, table_name: str, columns: list[str] | None = None) -> tuple[str, ...]:
    create_sql, temp_name = FOREIGN_KEY_REBUILDS[table_name]
    copy_sql = copy_columns_sql(columns or table_columns(conn, table_name), table_name, temp_name)
    return (
        f"DROP TABLE IF EXISTS {quote_ident(temp_name)}",
        create_sql,
        copy_sql,
        f"DROP TABLE {quote_ident(table_name)}",
        f"ALTER TABLE {quote_ident(temp_name)} RENAME TO {quote_ident(table_name)}",
    )


def migration_plan(conn: sqlite3.Connection) -> list[tuple[str, str, tuple[str, ...]]]:
    plan: list[tuple[str, str, tuple[str, ...]]] = []
    rebuilt_tables: set[str] = set()
    for old_name, new_name in TABLE_RENAMES:
        if not object_exists(conn, "table", old_name):
            continue
        old_sql_has_legacy_loader_fk = old_name in {
            "adp" + "lite_loader_trigger",
            "adp" + "lite_loader_run",
            "adp" + "lite_loader_event",
            "adp" + "lite_loader_state",
        } and table_sql_contains(conn, old_name, "REFERENCES adplite_loader")
        old_columns = table_columns(conn, old_name)
        if object_exists(conn, "table", new_name):
            if table_row_count(conn, new_name) != 0:
                raise RuntimeError(f"cannot migrate {old_name}: {new_name} already has rows")
            copy_sql = copy_table_sql(conn, old_name, new_name)
            drop_sql = f"DROP TABLE {quote_ident(old_name)}"
            plan.append(("copy-drop-table", old_name, (copy_sql, drop_sql)))
            if old_sql_has_legacy_loader_fk and new_name in FOREIGN_KEY_REBUILDS:
                plan.append(("rebuild-table", new_name, rebuild_table_plan(conn, new_name, old_columns)))
                rebuilt_tables.add(new_name)
            continue
        rename_sql = f"ALTER TABLE {quote_ident(old_name)} RENAME TO {quote_ident(new_name)}"
        plan.append(("rename-table", old_name, (rename_sql,)))
        if old_sql_has_legacy_loader_fk and new_name in FOREIGN_KEY_REBUILDS:
            plan.append(("rebuild-table", new_name, rebuild_table_plan(conn, new_name, old_columns)))
            rebuilt_tables.add(new_name)
    for index_name in INDEX_DROPS:
        if object_exists(conn, "index", index_name):
            sql = f"DROP INDEX IF EXISTS {quote_ident(index_name)}"
            plan.append(("drop-index", index_name, (sql,)))
    for table_name in FOREIGN_KEY_REBUILDS:
        if table_name not in rebuilt_tables and object_exists(conn, "table", table_name) and table_sql_contains(conn, table_name, "REFERENCES adplite_loader"):
            plan.append(("rebuild-table", table_name, rebuild_table_plan(conn, table_name)))
    return plan


def backup_database(db_path: Path) -> Path:
    backup_path = db_path.with_suffix(db_path.suffix + ".bak")
    counter = 1
    while backup_path.exists():
        backup_path = db_path.with_suffix(db_path.suffix + f".bak.{counter}")
        counter += 1
    shutil.copy2(db_path, backup_path)
    return backup_path


def main() -> int:
    args = parse_args()
    db_path = args.db_path.expanduser().resolve()
    if not db_path.exists():
        print(f"database not found: {db_path}", file=sys.stderr)
        return 1
    if not db_path.is_file():
        print(f"database path is not a file: {db_path}", file=sys.stderr)
        return 1

    conn = sqlite3.connect(str(db_path))
    try:
        conn.execute("PRAGMA foreign_keys = OFF")
        plan = migration_plan(conn)
        if not plan:
            print("no schema changes needed")
            return 0
        for kind, name, sqls in plan:
            for sql in sqls:
                print(f"{kind}: {name}: {sql}")
        if args.dry_run:
            return 0

        if not args.no_backup:
            backup_path = backup_database(db_path)
            print(f"backup: {backup_path}")

        with conn:
            for _, _, sqls in plan:
                for sql in sqls:
                    conn.execute(sql)
            conn.execute("PRAGMA user_version = user_version")
        print("migration complete")
        return 0
    finally:
        conn.close()


if __name__ == "__main__":
    raise SystemExit(main())
