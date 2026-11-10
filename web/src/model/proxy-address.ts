// Recognises a pasted proxy address so the proxy form can split it into its
// fields. Pure; no Vue imports so it can be unit-tested with node.

export interface ParsedProxyAddress {
  /** Only set when the text names a scheme this app supports. */
  scheme?: 'http' | 'https' | 'socks5' | 'socks5h'
  host: string
  port?: number
  username?: string
  password?: string
}

const schemeAliases: Record<string, ParsedProxyAddress['scheme']> = {
  http: 'http',
  https: 'https',
  socks5: 'socks5',
  socks5h: 'socks5h',
  socks: 'socks5',
}

const decode = (value: string): string => {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function parsePort(raw: string | undefined): number | null | undefined {
  if (raw === undefined || raw === '') return undefined
  if (!/^\d{1,5}$/.test(raw)) return null
  const port = Number(raw)
  return port >= 1 && port <= 65535 ? port : null
}

/**
 * Accepts `scheme://user:pass@host:port`, `user:pass@host:port`,
 * `host:port:user:pass` (proxy-list style) and `host:port`. Returns null when
 * the text is just a host (nothing to split) or cannot be read as an address.
 */
export function parseProxyAddress(text: string): ParsedProxyAddress | null {
  let rest = text.trim()
  if (!rest || /\s/.test(rest)) return null
  const result: ParsedProxyAddress = { host: '' }

  const schemeMatch = /^([a-z][a-z0-9+.-]*):\/\/(.*)$/i.exec(rest)
  if (schemeMatch) {
    const scheme = schemeAliases[schemeMatch[1]!.toLowerCase()]
    if (scheme) result.scheme = scheme
    rest = schemeMatch[2]!
    rest = rest.replace(/\/.*$/, '') // drop any path/query
  }
  if (!rest) return null

  const at = rest.lastIndexOf('@')
  if (at !== -1) {
    const credentials = rest.slice(0, at)
    rest = rest.slice(at + 1)
    const colon = credentials.indexOf(':')
    result.username = decode(colon === -1 ? credentials : credentials.slice(0, colon))
    if (colon !== -1) result.password = decode(credentials.slice(colon + 1))
  }

  const parts = rest.split(':')
  if (parts.length === 4 && result.username === undefined) {
    // host:port:user:pass
    result.username = parts[2]!
    result.password = parts[3]!
    parts.length = 2
  }
  if (parts.length > 2 || !parts[0]) return null
  result.host = parts[0]!
  const port = parsePort(parts[1])
  if (port === null) return null
  if (port !== undefined) result.port = port

  // A bare host has nothing to split; leave it to the user as typed.
  const splitSomething = result.scheme !== undefined || result.port !== undefined || result.username !== undefined
  return splitSomething ? result : null
}
