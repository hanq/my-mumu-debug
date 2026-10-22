# -*- coding: utf-8 -*-
"""mitmproxy addon: 每个事件输出一行 JSON 到 stdout, 供桌面工具解析。
设计要点见 docs/research/03-http-capture.md
"""
import json
import sys
from datetime import datetime

from mitmproxy import http, tls

MAX_BODY = 8000
BIN_CT = ("image/", "font", "octet-stream", "video/", "audio/")


def _ts():
    return datetime.now().strftime("%m-%d %H:%M:%S")


def _emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def _body_text(msg, pretty=False):
    try:
        raw = msg.raw_content
        if raw is None:
            return "", False
        ct = (msg.headers.get("content-type") or "").lower()
        if any(x in ct for x in BIN_CT):
            return "<binary %d bytes, %s>" % (len(raw), ct), False
        text = msg.get_text(strict=False) or ""
        if pretty and "json" in ct:
            try:
                text = json.dumps(json.loads(text), ensure_ascii=False, indent=2)
            except Exception:
                pass
        if len(text) > MAX_BODY:
            return text[:MAX_BODY], True
        return text, False
    except Exception as e:
        return "<body decode error: %s>" % e, False


def _headers(msg):
    skip = {"host", "content-length", "connection", "accept-encoding"}
    return {k: v for k, v in msg.headers.items() if k.lower() not in skip}


def response(flow: http.HTTPFlow):
    r = flow.response
    dur = 0
    try:
        dur = int((r.timestamp_end - flow.request.timestamp_start) * 1000)
    except Exception:
        pass
    body, truncated = _body_text(r, pretty=True)
    _emit({
        "type": "flow",
        "ts": _ts(),
        "method": flow.request.method,
        "url": flow.request.pretty_url,
        "status": r.status_code,
        "duration_ms": dur,
        "req_headers": _headers(flow.request),
        "resp_headers": _headers(r),
        "resp_body": body,
        "truncated": truncated,
    })


def request(flow: http.HTTPFlow):
    # 仅有请求暂无响应时也上报(便于观察 pending)
    if flow.response is not None:
        return
    body, truncated = _body_text(flow.request, pretty=True)
    _emit({
        "type": "request",
        "ts": _ts(),
        "method": flow.request.method,
        "url": flow.request.pretty_url,
        "status": 0,
        "duration_ms": 0,
        "req_headers": _headers(flow.request),
        "req_body": body,
        "truncated": truncated,
    })


def websocket_message(flow: http.HTTPFlow):
    if flow.websocket is None or not flow.websocket.messages:
        return
    msg = flow.websocket.messages[-1]
    try:
        content = msg.content.decode("utf-8", "replace")
    except Exception:
        content = "<binary %d bytes>" % len(msg.content)
    _emit({
        "type": "ws",
        "ts": _ts(),
        "direction": "C->S" if msg.from_client else "S->C",
        "url": flow.request.pretty_url,
        "content": content[:2000],
    })


def tls_failed_client(data: tls.TlsData):
    sni = getattr(data.conn, "sni", None) or "?"
    err = getattr(data, "client_error", None) or getattr(data.conn, "error", None)
    _emit({"type": "tls_fail", "ts": _ts(), "side": "client", "sni": str(sni), "error": str(err)})


def tls_failed_server(data: tls.TlsData):
    sni = getattr(data.conn, "sni", None) or "?"
    err = getattr(data, "server_error", None) or getattr(data.conn, "error", None)
    _emit({"type": "tls_fail", "ts": _ts(), "side": "server", "sni": str(sni), "error": str(err)})
