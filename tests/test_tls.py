"""Tests for TLS/SSL certificate health checks."""

from datetime import datetime, timedelta, timezone
from unittest.mock import MagicMock, patch

from medic.checks.tls import TLSCheck
from medic.core.enums import CheckCategory, CheckStatus


def test_tls_san_matching():
    chk = TLSCheck(host="example.com")
    assert chk._matches_san("example.com", ["example.com", "www.example.com"]) is True
    assert chk._matches_san("api.example.com", ["*.example.com", "example.com"]) is True
    assert chk._matches_san("other.org", ["example.com", "*.example.com"]) is False
    assert chk._matches_san("EXAMPLE.COM", ["example.com"]) is True


@patch("ssl.create_default_context")
@patch("socket.create_connection")
def test_tls_check_valid_cert(mock_conn, mock_ctx_factory):
    mock_ctx = MagicMock()
    mock_ssock = MagicMock()
    mock_ctx.wrap_socket.return_value = mock_ssock
    mock_ctx_factory.return_value = mock_ctx

    mock_ssock.cipher.return_value = ("TLS_AES_256_GCM_SHA384", "TLSv1.3", 256)
    mock_ssock.version.return_value = "TLSv1.3"
    mock_ssock.getpeercert.side_effect = lambda binary_form=False: (
        None if binary_form else {
            "notAfter": (datetime.now(timezone.utc) + timedelta(days=60)).strftime("%b %d %H:%M:%S %Y GMT"),
            "subjectAltName": [("DNS", "example.com")],
        }
    )

    chk = TLSCheck(host="example.com", port=443, warn_days=30, crit_days=7)
    res = chk.run()

    assert res.status == CheckStatus.PASSED
    assert res.category == CheckCategory.TLS
    assert "TLS Valid" in res.message
    assert res.details.get("days_remaining") is not None
    assert res.details["days_remaining"] >= 58


@patch("ssl.create_default_context")
@patch("socket.create_connection")
def test_tls_check_expiring_soon_warning(mock_conn, mock_ctx_factory):
    mock_ctx = MagicMock()
    mock_ssock = MagicMock()
    mock_ctx.wrap_socket.return_value = mock_ssock
    mock_ctx_factory.return_value = mock_ctx

    mock_ssock.cipher.return_value = ("TLS_AES_256_GCM_SHA384", "TLSv1.3", 256)
    mock_ssock.version.return_value = "TLSv1.3"
    mock_ssock.getpeercert.side_effect = lambda binary_form=False: (
        None if binary_form else {
            "notAfter": (datetime.now(timezone.utc) + timedelta(days=15)).strftime("%b %d %H:%M:%S %Y GMT"),
            "subjectAltName": [("DNS", "example.com")],
        }
    )

    chk = TLSCheck(host="example.com", port=443, warn_days=30, crit_days=7)
    res = chk.run()

    assert res.status == CheckStatus.WARNING
    assert "Plan renewal soon" in res.remediation
