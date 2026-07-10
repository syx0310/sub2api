import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('admin usage body size locale keys', () => {
  it('contains zh labels for body sizes', () => {
    expect(zh.admin.usage.bodySize).toBe('Body 大小')
    expect(zh.admin.usage.requestBodySize).toBe('请求')
    expect(zh.admin.usage.responseBodySize).toBe('返回')
  })

  it('contains en labels for body sizes', () => {
    expect(en.admin.usage.bodySize).toBe('Body Size')
    expect(en.admin.usage.requestBodySize).toBe('Req')
    expect(en.admin.usage.responseBodySize).toBe('Resp')
  })
})
