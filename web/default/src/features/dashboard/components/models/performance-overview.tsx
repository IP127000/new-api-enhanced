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
import { Activity, Gauge, HeartPulse, Timer } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { getDefaultDays } from '@/features/dashboard/lib'
import type { DashboardFilters } from '@/features/dashboard/types'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import type { PerfModelSummary } from '@/features/performance-metrics/types'
import { formatCompactNumber, formatNumber } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'

const TOP_MODEL_LIMIT = 6
const LOW_SAMPLE_REQUESTS = 10
const LOADING_METRIC_KEYS = [
  'perf-overview-success-rate',
  'perf-overview-requests',
  'perf-overview-latency',
]

type WeightedMetric = 'avg_latency_ms' | 'avg_tps'

type PerformanceSummary = {
  totalRequests: number
  avgLatencyMs: number
  avgTps: number
  successRate: number
}

function weightedAverage(
  rows: PerfModelSummary[],
  metric: WeightedMetric,
  isValid: (value: number) => boolean
): number {
  let total = 0
  let weightTotal = 0

  for (const row of rows) {
    const value = Number(row[metric])
    const weight = Number(row.request_count) || 0
    if (!isValid(value) || weight <= 0) continue
    total += value * weight
    weightTotal += weight
  }

  return weightTotal > 0 ? total / weightTotal : Number.NaN
}

function buildPerformanceSummary(rows: PerfModelSummary[]): PerformanceSummary {
  const totalRequests = rows.reduce(
    (total, row) => total + (Number(row.request_count) || 0),
    0
  )
  const totalSuccesses = rows.reduce(
    (total, row) => total + (Number(row.success_count) || 0),
    0
  )
  const successRate =
    totalRequests > 0 ? (totalSuccesses / totalRequests) * 100 : Number.NaN

  return {
    totalRequests,
    avgLatencyMs: Math.round(
      weightedAverage(
        rows,
        'avg_latency_ms',
        (value) => Number.isFinite(value) && value > 0
      )
    ),
    avgTps: weightedAverage(
      rows,
      'avg_tps',
      (value) => Number.isFinite(value) && value > 0
    ),
    successRate,
  }
}

export function PerformanceOverview(props: { filters?: DashboardFilters }) {
  const { t, i18n } = useTranslation()
  const locale = i18n.resolvedLanguage || i18n.language
  const timeRange = useMemo(
    () =>
      computeTimeRange(
        getDefaultDays(props.filters?.time_granularity),
        props.filters?.start_timestamp,
        props.filters?.end_timestamp
      ),
    [
      props.filters?.end_timestamp,
      props.filters?.start_timestamp,
      props.filters?.time_granularity,
    ]
  )
  const metricsQuery = useQuery({
    queryKey: [
      'perf-metrics-summary',
      timeRange.start_timestamp,
      timeRange.end_timestamp,
    ],
    queryFn: () => getPerfMetricsSummary(timeRange),
    staleTime: 60 * 1000,
    retry: false,
  })

  const models = useMemo(
    () => metricsQuery.data?.data.models ?? [],
    [metricsQuery.data]
  )
  const summary = useMemo(() => buildPerformanceSummary(models), [models])
  const topModels = useMemo(() => models.slice(0, TOP_MODEL_LIMIT), [models])
  const loading = metricsQuery.isLoading
  const hasData = models.length > 0

  if (!loading && !hasData) {
    return (
      <div className='text-muted-foreground overflow-hidden rounded-lg border px-4 py-3 text-center text-xs'>
        {t('No performance data available')}
      </div>
    )
  }

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center gap-x-5 gap-y-2.5 px-4 py-2.5 sm:px-5 sm:py-3'>
        {/* Title */}
        <div className='flex items-center gap-1.5'>
          <HeartPulse
            className='text-muted-foreground/60 size-3.5 shrink-0'
            aria-hidden='true'
          />
          <span className='text-xs font-semibold whitespace-nowrap'>
            {t('Performance health')}
          </span>
        </div>

        {/* Separator */}
        <div className='bg-border hidden h-4 w-px sm:block' />

        {/* KPI inline metrics */}
        {loading ? (
          <div className='flex flex-wrap items-center gap-x-5 gap-y-2'>
            {LOADING_METRIC_KEYS.map((key) => (
              <div key={key} className='flex items-center gap-1.5'>
                <Skeleton className='h-3 w-14' />
                <Skeleton className='h-4 w-16' />
              </div>
            ))}
          </div>
        ) : (
          <div className='flex flex-wrap items-center gap-x-5 gap-y-2'>
            <InlineMetric
              icon={HeartPulse}
              label={t('Success rate')}
              value={formatUptimePct(summary.successRate)}
              valueClassName={getSuccessRateTextClass(summary.successRate)}
            />
            <InlineMetric
              icon={Activity}
              label={t('Requests')}
              value={formatNumber(summary.totalRequests, locale)}
            />
            <InlineMetric
              icon={Timer}
              label={t('Average latency')}
              value={formatLatency(summary.avgLatencyMs)}
            />
            <InlineMetric
              icon={Gauge}
              label={t('Throughput')}
              value={formatThroughput(summary.avgTps)}
            />
          </div>
        )}

        {/* Separator */}
        <div className='bg-border hidden h-4 w-px lg:block' />

        {/* Top models inline badges */}
        {!loading && hasData && (
          <div className='flex flex-wrap items-center gap-1.5'>
            {topModels.map((model) => (
              <ModelBadge
                key={model.model_name}
                model={model}
                locale={locale}
                requestsLabel={t('Requests')}
                lowSampleLabel={t('Low sample')}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function InlineMetric(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: string
  valueClassName?: string
}) {
  const Icon = props.icon

  return (
    <div className='flex items-center gap-1.5'>
      <Icon
        className='text-muted-foreground/50 size-3 shrink-0'
        aria-hidden='true'
      />
      <span className='text-muted-foreground text-[11px]'>{props.label}</span>
      <span
        className={cn(
          'font-mono text-xs font-semibold tabular-nums',
          props.valueClassName
        )}
      >
        {props.value}
      </span>
    </div>
  )
}

function ModelBadge(props: {
  model: PerfModelSummary
  locale: Intl.LocalesArgument
  requestsLabel: string
  lowSampleLabel: string
}) {
  const model = props.model
  const requestCount = Number(model.request_count) || 0
  const successCount = Number(model.success_count) || 0
  const isLowSample = requestCount > 0 && requestCount < LOW_SAMPLE_REQUESTS
  const title = `${model.model_name}: ${formatNumber(successCount, props.locale)}/${formatNumber(requestCount, props.locale)} ${props.requestsLabel}${isLowSample ? `, ${props.lowSampleLabel}` : ''}`

  return (
    <span
      className={cn(
        'bg-muted/50 inline-flex items-center gap-1.5 rounded-full px-2.5 py-1',
        isLowSample && 'ring-muted-foreground/20 ring-1'
      )}
      title={title}
    >
      <span className='max-w-[10rem] truncate font-mono text-[11px]'>
        {model.model_name}
      </span>
      <span
        className={cn(
          'size-1.5 rounded-full',
          getSuccessRateDotClass(model.success_rate)
        )}
        aria-hidden='true'
      />
      <span
        className={cn(
          'font-mono text-[11px] font-semibold tabular-nums',
          getSuccessRateTextClass(model.success_rate)
        )}
      >
        {formatUptimePct(model.success_rate)}
      </span>
      <span className='text-muted-foreground font-mono text-[10px] tabular-nums'>
        {formatCompactNumber(requestCount, props.locale)}
      </span>
    </span>
  )
}
