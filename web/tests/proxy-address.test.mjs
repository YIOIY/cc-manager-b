import { test } from 'vitest'
import assert from 'node:assert/strict'
import { parseProxyAddress } from '../src/model/proxy-address.ts'

test('a full proxy URL is split into scheme, credentials, host and port', () => {
  assert.deepEqual(parseProxyAddress('http://JLvTg9sVZpcG:t2nXh7HWSrN9@204.1.88.141:7690'), {
    scheme: 'http',
    host: '204.1.88.141',
    port: 7690,
    username: 'JLvTg9sVZpcG',
    password: 't2nXh7HWSrN9',
  })
  assert.deepEqual(parseProxyAddress(' socks5h://proxy.example.com:1080 '), {
    scheme: 'socks5h',
    host: 'proxy.example.com',
    port: 1080,
  })
  // percent-encoded credentials are decoded; "socks" means socks5
  assert.deepEqual(parseProxyAddress('socks://u%40x:p%3Aw@1.2.3.4:1080'), {
    scheme: 'socks5',
    host: '1.2.3.4',
    port: 1080,
    username: 'u@x',
    password: 'p:w',
  })
})

test('scheme-less and list-style formats are recognised too', () => {
  assert.deepEqual(parseProxyAddress('user:pass@10.0.0.1:3128'), {
    host: '10.0.0.1',
    port: 3128,
    username: 'user',
    password: 'pass',
  })
  assert.deepEqual(parseProxyAddress('10.0.0.1:3128:user:pass'), {
    host: '10.0.0.1',
    port: 3128,
    username: 'user',
    password: 'pass',
  })
  assert.deepEqual(parseProxyAddress('10.0.0.1:3128'), { host: '10.0.0.1', port: 3128 })
})

test('plain hosts and junk are left alone', () => {
  assert.equal(parseProxyAddress('204.1.88.141'), null)
  assert.equal(parseProxyAddress('proxy.example.com'), null)
  assert.equal(parseProxyAddress(''), null)
  assert.equal(parseProxyAddress('http://'), null)
  assert.equal(parseProxyAddress('1.2.3.4:99999'), null)
  // unknown scheme: host/port still split, scheme left for the user
  assert.deepEqual(parseProxyAddress('ftp://1.2.3.4:21'), { host: '1.2.3.4', port: 21 })
})
