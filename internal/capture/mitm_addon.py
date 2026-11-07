# -*- coding: utf-8 -*-
"""mitmproxy addon: 每个事件输出一行 JSON 到 stdout, 供桌面工具解析。
设计要点见 docs/research/03-http-capture.md
"""
import base64
import json
import os
import sys
import tempfile
import time
from datetime import datetime
from itertools import count

from mitmproxy import http, tls

# Windows 管道下 stdout 默认跟随 ANSI 代码页(GBK), 强制 UTF-8 防止中文乱码
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

MAX_BODY = 8000
MAX_MEDIA_BODY = 8 * 1024 * 1024  # 图片/音视频内联 base64 的单 body 上限(8MB), 超出仅记占位
MAX_SAVE_BODY = 256 * 1024 * 1024  # 完整响应体落盘上限(256MB)
BIN_CT = ("image/", "font", "octet-stream", "video/", "audio/")
# 完整响应体临时目录(与 Go 侧 os.TempDir() 一致): 每次开抓包由 Go 清理旧文件
BODY_DIR = os.path.join(tempfile.gettempdir(), "my-mumu-debug", "flowbodies")
# 暂停标记文件(由桌面端写入/删除): 存在即暂停上报, 流量仍正常转发
PAUSE_FILE = os.path.join(tempfile.gettempdir(), "my-mumu-debug_capture_paused")
FLOW_IDS = count(1)

_pause_cache = {"checked_at": 0.0, "paused": False}


def _paused():
    """暂停状态: 最多每 0.3s 查一次文件, 避免每个请求都做系统调用。"""
    now = time.monotonic()
    if now - _pause_cache["checked_at"] > 0.3:
        try:
            _pause_cache["paused"] = os.path.exists(PAUSE_FILE)
        except Exception:
            _pause_cache["paused"] = False
        _pause_cache["checked_at"] = now
    return _pause_cache["paused"]


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
        if "\ufffd" in text:
            # 按声明字符集解码出现替换符(常见: 未声明 charset 的 GBK 报文), 尝试 GBK 并取替换符更少者
            gbk = raw.decode("gbk", errors="replace")
            if gbk.count("\ufffd") < text.count("\ufffd"):
                text = gbk
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
    # 完整输出全部请求/响应头(调试场景需完整可见, 不再过滤 host/content-length/connection/accept-encoding)
    return dict(msg.headers)


def _content_type(msg):
    return (msg.headers.get("content-type") or "").split(";")[0].strip().lower()


def response(flow: http.HTTPFlow):
    if _paused():
        return
    r = flow.response
    dur = 0
    try:
        dur = int((r.timestamp_end - flow.request.timestamp_start) * 1000)
    except Exception:
        pass
    body, truncated = _body_text(r, pretty=True)
    req_body, req_truncated = _body_text(flow.request, pretty=True)
    ct = _content_type(r)
    # 完整响应体(解压后原始字节): 落盘供前端"另存为", 同时供媒体预览内联 base64
    raw = None
    try:
        raw = r.get_content()
    except Exception:
        raw = None
    body_file = ""
    if raw and len(raw) <= MAX_SAVE_BODY:
        try:
            os.makedirs(BODY_DIR, exist_ok=True)
            body_file = os.path.join(BODY_DIR, "%d.body" % next(FLOW_IDS))
            with open(body_file, "wb") as fp:
                fp.write(raw)
        except Exception:
            body_file = ""
    ev = {
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
        "req_body": req_body,
        "req_truncated": req_truncated,
        "resp_content_type": ct,
        "resp_body_b64": "",
        "resp_body_file": body_file,
        "resp_body_size": len(raw) if raw else 0,
    }
    # 图片/音视频内联 base64 供前端预览
    if raw and 0 < len(raw) <= MAX_MEDIA_BODY and any(x in ct for x in ("image/", "video/", "audio/")):
        ev["resp_body_b64"] = base64.b64encode(raw).decode("ascii")
    _emit(ev)


def request(flow: http.HTTPFlow):
    # 仅有请求暂无响应时也上报(便于观察 pending)
    if _paused() or flow.response is not None:
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
    if _paused() or flow.websocket is None or not flow.websocket.messages:
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
    if _paused():
        return
    sni = getattr(data.conn, "sni", None) or "?"
    err = getattr(data, "client_error", None) or getattr(data.conn, "error", None)
    _emit({"type": "tls_fail", "ts": _ts(), "side": "client", "sni": str(sni), "error": str(err)})


def tls_failed_server(data: tls.TlsData):
    if _paused():
        return
    sni = getattr(data.conn, "sni", None) or "?"
    err = getattr(data, "server_error", None) or getattr(data.conn, "error", None)
    _emit({"type": "tls_fail", "ts": _ts(), "side": "server", "sni": str(sni), "error": str(err)})
