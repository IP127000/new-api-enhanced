import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { QuotaDataItem } from '../types'
import { processChartData } from './charts'

describe('dashboard consumption distribution', () => {
  test('uses token consumption instead of quota-derived currency', () => {
    const rows: QuotaDataItem[] = [
      {
        created_at: 1_750_000_000,
        model_name: 'model-a',
        quota: 500_000,
        token_used: 125,
        count: 1,
      },
      {
        created_at: 1_750_000_000,
        model_name: 'model-b',
        quota: 1,
        token_used: 875,
        count: 1,
      },
    ]

    const result = processChartData(rows, 'hour', (key) => key)
    const barValues = result.spec_line.data[0].values.filter(
      (item: Record<string, number>) => item.Tokens > 0
    )
    const areaValues = result.spec_area.data[0].values.filter(
      (item: Record<string, number>) => item.Tokens > 0
    )

    assert.equal(result.spec_line.yField, 'Tokens')
    assert.equal(result.spec_area.yField, 'Tokens')
    assert.deepEqual(
      barValues.map((item: Record<string, number>) => item.Tokens),
      [875, 125]
    )
    assert.equal(
      areaValues.reduce(
        (sum: number, item: Record<string, number>) => sum + item.Tokens,
        0
      ),
      1_000
    )
    assert.equal(
      result.totalTokensDisplay,
      Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(1_000)
    )
    assert.equal(JSON.stringify(result.spec_line).includes('$'), false)
    assert.equal(JSON.stringify(result.spec_line).includes('rawQuota'), false)
  })
})
