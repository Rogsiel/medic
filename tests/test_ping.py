"""Tests for ping health checks."""

from unittest.mock import MagicMock, patch

from medic.checks.ping import PingCheck
from medic.core.enums import CheckCategory, CheckStatus


def test_ping_check_output_parsing_linux():
    chk = PingCheck(host="1.1.1.1")
    stdout = """
PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.
64 bytes from 1.1.1.1: icmp_seq=1 ttl=59 time=12.4 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=59 time=11.8 ms
64 bytes from 1.1.1.1: icmp_seq=3 ttl=59 time=12.1 ms

--- 1.1.1.1 ping statistics ---
3 packets transmitted, 3 received, 0% packet loss, time 2003ms
rtt min/avg/max/mdev = 11.800/12.100/12.400/0.245 ms
"""
    parsed = chk._parse_output(stdout)
    assert parsed["transmitted"] == 3
    assert parsed["received"] == 3
    assert parsed["packet_loss_pct"] == 0.0
    assert parsed["min_rtt_ms"] == 11.8
    assert parsed["avg_rtt_ms"] == 12.1
    assert parsed["max_rtt_ms"] == 12.4
    assert parsed["mdev_rtt_ms"] == 0.245
    assert parsed["resolved_ip"] == "1.1.1.1"


def test_ping_check_output_parsing_windows():
    chk = PingCheck(host="8.8.8.8")
    stdout = """
Pinging 8.8.8.8 with 32 bytes of data:
Reply from 8.8.8.8: bytes=32 time=15ms TTL=115
Reply from 8.8.8.8: bytes=32 time=14ms TTL=115

Ping statistics for 8.8.8.8:
    Packets: Sent = 2, Received = 2, Lost = 0 (0% loss),
Approximate round trip times in milli-seconds:
    Minimum = 14ms, Maximum = 15ms, Average = 14ms
"""
    parsed = chk._parse_output(stdout)
    assert parsed["transmitted"] == 2
    assert parsed["received"] == 2
    assert parsed["packet_loss_pct"] == 0.0
    assert parsed["min_rtt_ms"] == 14.0
    assert parsed["avg_rtt_ms"] == 14.0
    assert parsed["max_rtt_ms"] == 15.0


def test_ping_check_real_localhost():
    chk = PingCheck(host="127.0.0.1", count=2, timeout_sec=2.0)
    res = chk.run()
    assert res.category == CheckCategory.PING
    assert res.target == "127.0.0.1"
    assert res.status == CheckStatus.PASSED
    assert res.latency_ms is not None
    assert res.details.get("packet_loss_pct") == 0.0


@patch("subprocess.run")
def test_ping_check_packet_loss_warning(mock_sub):
    stdout = """
3 packets transmitted, 2 received, 33.3% packet loss, time 2002ms
rtt min/avg/max/mdev = 10.0/12.0/14.0/1.0 ms
"""
    mock_sub.return_value = MagicMock(returncode=0, stdout=stdout, stderr="")
    chk = PingCheck(host="192.168.1.100")
    res = chk.run()
    assert res.status == CheckStatus.WARNING
    assert "Packet loss: 33.3%" in res.message


@patch("subprocess.run")
def test_ping_check_total_failure(mock_sub):
    stdout = """
3 packets transmitted, 0 received, 100% packet loss, time 2002ms
"""
    mock_sub.return_value = MagicMock(returncode=1, stdout=stdout, stderr="")
    chk = PingCheck(host="10.255.255.1")
    res = chk.run()
    assert res.status == CheckStatus.FAILED
    assert "100% packet loss" in res.message
    assert res.remediation is not None
