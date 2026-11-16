"""Tests for SSH service health checks."""

from unittest.mock import MagicMock, patch

from medic.checks.ssh import SSHCheck
from medic.core.enums import CheckCategory, CheckStatus


@patch("socket.create_connection")
def test_ssh_check_valid_openssh_banner(mock_conn):
    mock_sock = MagicMock()
    mock_conn.return_value = mock_sock

    banner_data = b"SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.4\r\n"
    # Return 1 byte at a time to simulate RFC banner streaming
    mock_sock.recv.side_effect = [bytes([b]) for b in banner_data] + [b""]

    chk = SSHCheck(host="192.168.1.10", port=22)
    res = chk.run()

    assert res.status == CheckStatus.PASSED
    assert res.category == CheckCategory.SSH
    assert "OpenSSH_9.6p1" in res.message
    assert res.details.get("protocol_version") == "2.0"
    assert res.details.get("software_version") == "OpenSSH_9.6p1"


@patch("socket.create_connection")
def test_ssh_check_legacy_insecure_ssh1(mock_conn):
    mock_sock = MagicMock()
    mock_conn.return_value = mock_sock

    banner_data = b"SSH-1.5-OldSSH_1.0\r\n"
    mock_sock.recv.side_effect = [bytes([b]) for b in banner_data] + [b""]

    chk = SSHCheck(host="192.168.1.10", port=22)
    res = chk.run()

    assert res.status == CheckStatus.FAILED
    assert "Insecure" in res.remediation


@patch("socket.create_connection", side_effect=ConnectionRefusedError("Connection refused"))
def test_ssh_check_connection_refused(mock_conn):
    chk = SSHCheck(host="127.0.0.1", port=22)
    res = chk.run()

    assert res.status == CheckStatus.FAILED
    assert "refused" in res.message
    assert res.remediation is not None
