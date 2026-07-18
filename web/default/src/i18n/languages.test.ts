import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { toIntlLocale } from './languages'

describe('toIntlLocale', () => {
  test('maps interface locales to valid Intl locales', () => {
    assert.equal(toIntlLocale('zhCN'), 'zh-CN')
    assert.equal(toIntlLocale('zhTW'), 'zh-TW')
    assert.equal(toIntlLocale('en'), 'en')
  })

  test('prevents Chinese interface locales from crashing Intl formatters', () => {
    assert.doesNotThrow(() =>
      new Intl.NumberFormat(toIntlLocale('zhCN')).format(12_345)
    )
  })

  test('falls back to the runtime locale for an invalid language tag', () => {
    assert.equal(toIntlLocale('not_a_locale'), undefined)
  })
})
