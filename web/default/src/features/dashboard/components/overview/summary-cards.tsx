/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StaggerContainer, StaggerItem } from '@/components/page-transition'
import {
  getHistoricalTokenStats,
  getUserQuotaDates,
} from '@/features/dashboard/api'
import { useSummaryCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { useIsAdmin } from '@/hooks/use-admin'
import { useStatus } from '@/hooks/use-status'
import { getCurrencyLabel, isCurrencyDisplayEnabled } from '@/lib/currency'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import { StatCard } from '../ui/stat-card'

const SUMMARY_SPARKLINE_BUCKETS = 12

type SummarySparklineKey = 'balance' | 'usage' | 'requests'

function getBucketIndex(
  timestamp: number,
  start: number,
  end: number,
  bucketCount: number
): number {
  if (end <= start) return 0
  const ratio = (timestamp - start) / (end - start)
  return Math.min(bucketCount - 1, Math.max(0, Math.floor(ratio * bucketCount)))
}

function buildSummarySparklines(
  data: QuotaDataItem[],
  currentBalance: number,
  start: number,
  end: number
): Record<SummarySparklineKey, number[]> {
  const usage = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)
  const requests = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)

  for (const item of data) {
    const timestamp = Number(item.created_at) || start
    const index = getBucketIndex(
      timestamp,
      start,
      end,
      SUMMARY_SPARKLINE_BUCKETS
    )
    usage[index] += Number(item.quota) || 0
    requests[index] += Number(item.count) || 0
  }

  let balance = currentBalance
  const balanceTrend = Array.from(
    { length: SUMMARY_SPARKLINE_BUCKETS },
    () => 0
  )

  for (let index = SUMMARY_SPARKLINE_BUCKETS - 1; index >= 0; index--) {
    balanceTrend[index] = Math.max(0, balance)
    balance += usage[index]
  }

  return {
    balance: balanceTrend,
    usage,
    requests,
  }
}

function getSummarySparkline(
  key: string,
  sparklineData: Record<SummarySparklineKey, number[]>
): number[] | undefined {
  if (key === 'usage') return sparklineData.usage
  if (key === 'requests') return sparklineData.requests
  return undefined
}

function formatHitRate(value: number | null | undefined): string {
  return Intl.NumberFormat(undefined, {
    style: 'percent',
    maximumFractionDigits: 2,
  }).format(value || 0)
}

export function SummaryCards() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { status, loading } = useStatus()
  const isAdmin = useIsAdmin()

  const summaryTimeRange = useMemo(() => computeTimeRange(1), [])
  const remainQuota = Number(user?.quota ?? 0)

  const usageTrendQuery = useQuery({
    queryKey: [
      'dashboard',
      'overview',
      'summary-sparklines',
      isAdmin,
      summaryTimeRange.start_timestamp,
      summaryTimeRange.end_timestamp,
    ],
    queryFn: async () =>
      getUserQuotaDates(
        {
          start_timestamp: summaryTimeRange.start_timestamp,
          end_timestamp: summaryTimeRange.end_timestamp,
          default_time: 'hour',
        },
        isAdmin
      ),
    staleTime: 60 * 1000,
  })

  const historicalTokenQuery = useQuery({
    queryKey: ['dashboard', 'overview', 'historical-token-stats', isAdmin],
    queryFn: () => getHistoricalTokenStats(isAdmin),
    staleTime: 60 * 1000,
  })

  const historicalTokens = Number(
    historicalTokenQuery.data?.data?.total_tokens ?? 0
  )
  const historicalPromptTokens = Number(
    historicalTokenQuery.data?.data?.prompt_tokens ?? 0
  )
  const historicalCompletionTokens = Number(
    historicalTokenQuery.data?.data?.completion_tokens ?? 0
  )
  const historicalCacheTokens = Number(
    historicalTokenQuery.data?.data?.cache_tokens ?? 0
  )
  const historicalCacheHitRate = Number(
    historicalTokenQuery.data?.data?.cache_hit_rate ?? 0
  )
  const historicalQuota = Number(historicalTokenQuery.data?.data?.quota ?? 0)
  const historicalRequestCount = Number(
    historicalTokenQuery.data?.data?.request_count ?? 0
  )

  const summaryValues = useMemo(() => {
    return {
      historicalTokensDisplay: formatNumber(historicalTokens),
      historicalInputTokensDisplay: formatNumber(historicalPromptTokens),
      historicalOutputTokensDisplay: formatNumber(historicalCompletionTokens),
      historicalCacheTokensDisplay: formatNumber(historicalCacheTokens),
      historicalCacheHitRateDisplay: formatHitRate(historicalCacheHitRate),
      usedDisplay: formatQuota(historicalQuota),
      requestCountDisplay: formatNumber(historicalRequestCount),
    }
  }, [
    historicalCacheHitRate,
    historicalCacheTokens,
    historicalCompletionTokens,
    historicalPromptTokens,
    historicalQuota,
    historicalRequestCount,
    historicalTokens,
  ])

  const currencyEnabledFromStore = isCurrencyDisplayEnabled()
  const statusCurrencyFlag =
    typeof status?.display_in_currency === 'boolean'
      ? Boolean(status.display_in_currency)
      : undefined
  const currencyEnabled =
    statusCurrencyFlag !== undefined
      ? statusCurrencyFlag
      : currencyEnabledFromStore
  const currencyLabel = currencyEnabled ? getCurrencyLabel() : 'Tokens'

  const sparklineData = useMemo(
    () =>
      buildSummarySparklines(
        usageTrendQuery.data?.data ?? [],
        remainQuota,
        summaryTimeRange.start_timestamp,
        summaryTimeRange.end_timestamp
      ),
    [
      remainQuota,
      summaryTimeRange.end_timestamp,
      summaryTimeRange.start_timestamp,
      usageTrendQuery.data?.data,
    ]
  )

  const items = useSummaryCardsConfig({
    ...summaryValues,
    currencyEnabled,
    currencyLabel,
  }).map((config, index) => {
    const tones = ['rose', 'teal', 'gray'] as const

    return {
      key: config.key,
      title: config.title,
      value: config.value,
      desc: config.description,
      icon: config.icon,
      details: config.details,
      tone: tones[index] ?? 'gray',
      sparkline:
        config.key === 'historicalTokens'
          ? undefined
          : getSummarySparkline(config.key, sparklineData),
      sparklineVariant: 'line' as const,
    }
  })
  const summaryLoading =
    loading || usageTrendQuery.isLoading || historicalTokenQuery.isLoading

  return (
    <div className='bg-card overflow-hidden rounded-2xl border shadow-xs'>
      <div className='flex flex-col gap-3 p-4 sm:p-5'>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div className='flex flex-col gap-1'>
            <h3 className='text-base font-semibold'>
              {t('Usage at a glance')}
            </h3>
            <p className='text-muted-foreground text-sm'>
              {t('Monitor token usage, spend, and request volume')}
            </p>
          </div>
        </div>
        <StaggerContainer className='grid gap-3 lg:grid-cols-2 2xl:grid-cols-3'>
          {items.map((it) => (
            <StaggerItem
              key={it.key}
              className={
                it.key === 'historicalTokens'
                  ? 'bg-background/60 rounded-xl border p-3 lg:col-span-2 2xl:col-span-1'
                  : 'bg-background/60 rounded-xl border p-3'
              }
            >
              <StatCard
                title={it.title}
                value={it.value}
                description={it.desc}
                icon={it.icon}
                tone={it.tone}
                details={it.details}
                sparkline={it.sparkline}
                sparklineVariant={it.sparklineVariant}
                loading={summaryLoading}
              />
            </StaggerItem>
          ))}
        </StaggerContainer>
      </div>
    </div>
  )
}
