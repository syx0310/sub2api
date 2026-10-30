import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'

// Use the real runtime-only bundle shared by Vite and Vitest, not a mocked t().
// Locale strings are loaded dynamically, so compiler-only tests are insufficient.
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('i18n runtime compatibility', () => {
  it('loads both application locales and switches translations', async () => {
    const { i18n, loadLocaleMessages } = await import('../index')
    const previousLocale = i18n.global.locale.value
    try {
      await loadLocaleMessages('en')
      await loadLocaleMessages('zh')
      i18n.global.locale.value = 'en'
      expect(i18n.global.t('common.cancel')).toBe('Cancel')
      i18n.global.locale.value = 'zh'
      expect(i18n.global.t('common.cancel')).toBe('取消')
    } finally {
      i18n.global.locale.value = previousLocale
    }
  })

  it('supports string messages without dynamic code evaluation under CSP', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      warnHtmlMessage: false,
      messages: {
        en: {
          named: 'Hello {name}',
          list: 'Item {0}',
          plural: 'no items | one item | {count} items',
          linked: '@:named',
          rich: '<strong>{name}</strong>'
        }
      }
    })
    const blockedFunction = vi.fn(() => { throw new Error('CSP blocks unsafe-eval') })
    vi.stubGlobal('Function', blockedFunction)

    expect(i18n.global.t('named', { name: 'Codex' })).toBe('Hello Codex')
    expect(i18n.global.t('list', ['A'])).toBe('Item A')
    expect(i18n.global.t('plural', 0)).toBe('no items')
    expect(i18n.global.t('plural', 1)).toBe('one item')
    expect(i18n.global.t('plural', 3)).toBe('3 items')
    expect(i18n.global.t('linked', { name: 'Codex' })).toBe('Hello Codex')
    expect(i18n.global.t('rich', { name: 'Codex' })).toBe('<strong>Codex</strong>')
    expect(blockedFunction).not.toHaveBeenCalled()
  })

  it('neutralizes srcdoc when escaped parameters are used in HTML messages', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      warnHtmlMessage: false,
      escapeParameter: true,
      messages: { en: { embedded: '<iframe srcdoc="{content}"></iframe>' } }
    })
    const result = i18n.global.t('embedded', { content: '<script>alert(1)</script>' })
    const fragment = document.createElement('template')
    fragment.innerHTML = result

    expect(fragment.content.querySelector('iframe')).not.toBeNull()
    expect(fragment.content.querySelector('iframe')!.hasAttribute('srcdoc')).toBe(false)
    expect(fragment.content.querySelector('script')).toBeNull()
  })
})
