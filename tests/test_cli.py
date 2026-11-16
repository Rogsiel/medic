"""Tests for CLI arguments, parser, and main execution."""

import json

from medic.cli import build_checks_from_args, build_parser, main, parse_status_codes, parse_threshold


def test_parse_threshold():
    assert parse_threshold("80,95", (50.0, 90.0)) == (80.0, 95.0)
    assert parse_threshold("75", (50.0, 90.0)) == (75.0, 90.0)
    assert parse_threshold("invalid", (50.0, 90.0)) == (50.0, 90.0)


def test_parse_status_codes():
    assert parse_status_codes("200,201") == [200, 201]
    assert 200 in parse_status_codes("200-204")
    assert 204 in parse_status_codes("200-204")
    assert 205 not in parse_status_codes("200-204")


def test_cli_parser_defaults():
    parser = build_parser()
    args = parser.parse_args([])
    assert args.all is False
    assert args.system is False
    assert args.format == "rich"
    assert args.concurrency is None
    assert args.fail_on is None
    assert args.watch is None


def test_cli_build_checks_all():
    parser = build_parser()
    args = parser.parse_args(["--all"])
    checks = build_checks_from_args(args)
    assert len(checks) >= 4


def test_cli_build_checks_specific():
    parser = build_parser()
    args = parser.parse_args([
        "--system",
        "--ping", "1.1.1.1", "8.8.8.8",
        "--tcp", "localhost:5432",
        "--http", "https://example.com",
        "--tls", "example.com:443",
        "--ssh", "localhost:22",
    ])
    checks = build_checks_from_args(args)
    # 1 system group + 2 pings + 1 tcp + 1 http + 1 tls + 1 ssh = 7 checks
    assert len(checks) == 7


def test_cli_main_system_json(capsys):
    ret = main(["--system", "--format", "json", "--quiet"])
    # Quiet mode with json
    assert ret in (0, 1)


def test_cli_main_json_output(capsys):
    ret = main(["--system", "--format", "json"])
    captured = capsys.readouterr()
    data = json.loads(captured.out)
    assert "summary" in data
    assert "results" in data
    assert data["summary"]["total"] >= 5
    assert ret == data["summary"]["exit_code"]
