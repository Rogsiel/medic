"""Tests for TCP connectivity health checks."""

import socket
import threading

from medic.checks.tcp import TCPCheck, parse_host_port
from medic.core.enums import CheckCategory, CheckStatus


def test_parse_host_port():
    assert parse_host_port("localhost:8080") == ("localhost", 8080)
    assert parse_host_port("1.1.1.1:53") == ("1.1.1.1", 53)
    assert parse_host_port("example.com") == ("example.com", 80)
    assert parse_host_port("example.com", default_port=443) == ("example.com", 443)
    assert parse_host_port("[::1]:9000") == ("::1", 9000)
    assert parse_host_port("[::1]") == ("::1", 80)


def test_tcp_check_real_open_socket():
    # Spin up a temporary local TCP listener
    server_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server_sock.bind(("127.0.0.1", 0))
    server_sock.listen(1)
    port = server_sock.getsockname()[1]

    def accept_one():
        try:
            client, _ = server_sock.accept()
            client.sendall(b"HELLO_MEDIC\n")
            client.close()
        except Exception:
            pass

    t = threading.Thread(target=accept_one, daemon=True)
    t.start()

    try:
        chk = TCPCheck(
            host="127.0.0.1",
            port=port,
            grab_banner=True,
            timeout=2.0,
        )
        res = chk.run()
        assert res.status == CheckStatus.PASSED
        assert res.category == CheckCategory.TCP
        assert res.details.get("banner") == "HELLO_MEDIC"
    finally:
        server_sock.close()


def test_tcp_check_connection_refused():
    # Use an unallocated high port on localhost
    chk = TCPCheck(host="127.0.0.1", port=59999, timeout=1.0)
    res = chk.run()
    assert res.status == CheckStatus.FAILED
    assert "refused" in res.message.lower()
    assert res.remediation is not None


def test_tcp_check_dns_failure():
    chk = TCPCheck(host="this-domain-does-not-exist-at-all-12345.xyz", port=80, timeout=1.0)
    res = chk.run()
    assert res.status == CheckStatus.FAILED
    assert "DNS" in res.message or "failed" in res.message
