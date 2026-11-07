// 前端类型定义(与 Go 绑定结构对应)

export interface EmuInstance {
  index: number
  name: string
  serial: string
  running: boolean
  android_started: boolean
  android_version: string
  pid: number
}

export interface InstallInfo {
  installed: boolean
  dir: string
  manager_path: string
  adb_path: string
}

export interface FlowEvent {
  type: 'flow' | 'request' | 'ws' | 'tls_fail'
  ts: string
  method: string
  url: string
  status: number
  duration_ms: number
  req_headers: Record<string, string>
  resp_headers: Record<string, string>
  req_body: string
  req_truncated: boolean
  resp_body: string
  /** 图片/音视频内联 base64(≤8MB, 空=未内联) */
  resp_body_b64: string
  /** 响应 content-type(去参数小写) */
  resp_content_type: string
  /** 完整响应体落盘路径(空=未落盘: pending/空 body/超 256MB) */
  resp_body_file: string
  /** 完整响应体字节数 */
  resp_body_size: number
  truncated: boolean
  direction: string
  content: string
  side: string
  sni: string
  error: string
  /** 前端本地行 id(pending 行被完成事件原地替换时保持 DOM 稳定) */
  _id?: number
}

export interface ProcConnection {
  local: string
  remote: string
  state: string
  pid: number
  proc: string
}
