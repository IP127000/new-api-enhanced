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
import { Activity, Gauge, Timer } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { getModelRequestCounts } from '@/features/dashboard/api'
import type { ModelRequestCount } from '@/features/dashboard/types'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import {
  formatLatency,
  formatThroughput,
} from '@/features/performance-metrics/lib/format'
import type { PerfModelSummary } from '@/features/performance-metrics/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber, formatNumber } from '@/lib/format'

const PERFORMANCE_WINDOW = 'all'
const TOP_MODEL_LIMIT = 6
const LOADING_ROW_KEYS = [
  'perf-health-row-1',
  'perf-health-row-2',
  'perf-health-row-3',
]

type WeightedMetric = 'avg_latency_ms' | 'avg_tps'

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
export function PerformanceHealthPanel() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const metricsQuery = useQuery({
    queryKey: ['perf-metrics-summary', PERFORMANCE_WINDOW],
    queryFn: () => getPerfMetricsSummary(PERFORMANCE_WINDOW),
    staleTime: 60 * 1000,
    retry: false,
  })

  const requestCountsQuery = useQuery({
    queryKey: ['log-model-request-counts', PERFORMANCE_WINDOW],
    queryFn: () => getModelRequestCounts(),
    staleTime: 60 * 1000,
    retry: false,
  })

  const metricModels = useMemo(
    () => metricsQuery.data?.data.models ?? [],
    [metricsQuery.data]
  )
  const requestRows = useMemo(
    () => requestCountsQuery.data?.data ?? [],
    [requestCountsQuery.data]
  )

  const summary = useMemo(() => {
    return {
      totalRequests: requestRows.reduce(
        (total, row) => total + (Number(row.request_count) || 0),
        0
      ),
      avgLatencyMs: Math.round(
        weightedAverage(
          metricModels,
          'avg_latency_ms',
          (v) => Number.isFinite(v) && v > 0
        )
      ),
      avgTps: weightedAverage(
        metricModels,
        'avg_tps',
        (v) => Number.isFinite(v) && v > 0
      ),
    }
  }, [metricModels, requestRows])

  const topModels = useMemo(
    () => requestRows.slice(0, TOP_MODEL_LIMIT),
    [requestRows]
  )
  const maxTopModelRequests = useMemo(
    () =>
      topModels.reduce(
        (max, model) => Math.max(max, Number(model.request_count) || 0),
        0
      ),
    [topModels]
  )
  const loading = metricsQuery.isLoading || requestCountsQuery.isLoading
  const hasData = requestRows.length > 0 || metricModels.length > 0

  return (
    <section className='bg-card h-full overflow-hidden rounded-2xl border shadow-xs'>
      <div className='flex items-center gap-2 border-b px-4 py-3 sm:px-5'>
        <Activity
          className='text-muted-foreground/60 size-4 shrink-0'
          aria-hidden='true'
        />
        <h3 className='text-sm font-semibold'>
          {t('Model performance metrics')}
        </h3>
        <span className='text-muted-foreground ml-auto text-xs'>
          {t('All-time')}
        </span>
      </div>

      <div className='flex flex-col gap-3 p-4 sm:p-5'>
        <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
          <MetricCell
            icon={Activity}
            label={t('Requests')}
            value={formatNumber(summary.totalRequests, locale)}
            loading={loading}
          />
          <MetricCell
            icon={Timer}
            label={t('Average latency')}
            value={formatLatency(summary.avgLatencyMs)}
            loading={loading}
          />
          <MetricCell
            icon={Gauge}
            label={t('Throughput')}
            value={formatThroughput(summary.avgTps)}
            loading={loading}
          />
        </div>

        {loading ? (
          <div className='flex flex-col gap-2'>
            {LOADING_ROW_KEYS.map((key) => (
              <Skeleton key={key} className='h-11 w-full rounded-lg' />
            ))}
          </div>
        ) : (
          hasData && (
            <div className='flex flex-col gap-2'>
              <div className='text-muted-foreground flex items-center justify-between gap-3 text-[11px] font-medium'>
                <span>{t('Top models by traffic')}</span>
                <span>{t('Requests')}</span>
              </div>
              <div className='flex flex-col gap-1.5'>
                {topModels.map((model) => (
                  <TopModelRow
                    key={model.model_name}
                    model={model}
                    locale={locale}
                    maxRequests={maxTopModelRequests}
                    requestsLabel={t('Requests')}
                  />
                ))}
              </div>
            </div>
          )
        )}
      </div>
    </section>
  )
}

function TopModelRow(props: {
  model: ModelRequestCount
  locale?: Intl.LocalesArgument
  maxRequests: number
  requestsLabel: string
}) {
  const requestCount = Number(props.model.request_count) || 0
  const trafficShare =
    props.maxRequests > 0
      ? Math.max(6, (requestCount / props.maxRequests) * 100)
      : 0

  return (
    <div
      className='bg-background/60 grid min-h-11 grid-cols-[minmax(0,1fr)_5rem] items-center gap-3 rounded-lg border px-3 py-2'
      title={`${props.model.model_name}: ${formatNumber(requestCount, props.locale)} ${props.requestsLabel}`}
    >
      <div className='min-w-0'>
        <div className='truncate font-mono text-xs font-medium'>
          {props.model.model_name}
        </div>
        <div className='bg-muted mt-1 h-1.5 overflow-hidden rounded-full'>
          <div
            className='bg-primary/65 h-full rounded-full'
            style={{ width: `${trafficShare}%` }}
          />
        </div>
      </div>
      <div className='text-muted-foreground text-right font-mono text-xs tabular-nums'>
        {formatCompactNumber(requestCount, props.locale)}
      </div>
    </div>
  )
}

function MetricCell(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: string
  loading: boolean
}) {
  const Icon = props.icon
  return (
    <div className='bg-background/60 flex min-h-20 flex-col justify-between rounded-xl border px-3 py-2.5'>
      <div className='text-muted-foreground flex items-center gap-1.5 text-[11px] font-medium'>
        <Icon className='size-3 shrink-0' aria-hidden='true' />
        <span className='truncate'>{props.label}</span>
      </div>
      {props.loading ? (
        <Skeleton className='mt-1.5 h-5 w-16' />
      ) : (
        <div className='mt-1.5 font-mono text-sm font-semibold tabular-nums'>
          {props.value}
        </div>
      )}
    </div>
  )
}
