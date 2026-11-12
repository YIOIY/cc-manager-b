import { afterEach, expect, test, vi } from 'vitest'
import { copyText } from '../src/utils/clipboard'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('uses the clipboard API when the page has it', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  await expect(copyText('hello')).resolves.toBe(true)
  expect(writeText).toHaveBeenCalledWith('hello')
})

test('over plain HTTP (no clipboard API) it copies through a temporary text area', async () => {
  vi.stubGlobal('navigator', {})
  let copied = ''
  const exec = vi.fn(() => {
    copied = (document.activeElement as HTMLTextAreaElement | null)?.value ?? ''
    return true
  })
  Object.defineProperty(document, 'execCommand', { value: exec, configurable: true })
  await expect(copyText('https://claude.com/cai/oauth/authorize?x=1')).resolves.toBe(true)
  expect(exec).toHaveBeenCalledWith('copy')
  expect(copied).toBe('https://claude.com/cai/oauth/authorize?x=1')
  expect(document.querySelectorAll('textarea')).toHaveLength(0)
})

test('falls back when the clipboard API refuses, and reports a total failure', async () => {
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } })
  Object.defineProperty(document, 'execCommand', { value: vi.fn(() => true), configurable: true })
  await expect(copyText('x')).resolves.toBe(true)
  Object.defineProperty(document, 'execCommand', { value: vi.fn(() => false), configurable: true })
  await expect(copyText('x')).resolves.toBe(false)
})
