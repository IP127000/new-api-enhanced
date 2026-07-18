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
import {
  Hash,
  Coins,
  Layers,
  LogIn,
  LogOut,
  DatabaseZap,
  Percent,
  TrendingUp,
  Activity,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

interface StatCardConfig {
  key: string
  title: string
  description: string
  icon: LucideIcon
  getValue: (stat: Record<string, number>, days?: number) => number
}

export function useModelStatCardsConfig(): StatCardConfig[] {
  const { t } = useTranslation()

  return [
    {
      key: 'count',
      title: t('Total Count'),
      description: t('Statistical count'),
      icon: Hash,
      getValue: (stat) => stat?.requestCount ?? 0,
    },
    {
      key: 'quota',
      title: t('Total Quota'),
      description: t('Statistical quota'),
      icon: Coins,
      getValue: (stat) => stat?.quota ?? 0,
    },
    {
      key: 'totalTokens',
      title: t('Total Tokens'),
      description: t('Statistical tokens'),
      icon: Layers,
      getValue: (stat) => stat?.totalTokens ?? 0,
    },
    {
      key: 'inputTokens',
      title: t('Input Tokens'),
      description: t('Input tokens in selected range'),
      icon: LogIn,
      getValue: (stat) => stat?.promptTokens ?? 0,
    },
    {
      key: 'outputTokens',
      title: t('Output Tokens'),
      description: t('Output tokens in selected range'),
      icon: LogOut,
      getValue: (stat) => stat?.completionTokens ?? 0,
    },
    {
      key: 'cacheTokens',
      title: t('Cache Hit Tokens'),
      description: t('Cache hit tokens in selected range'),
      icon: DatabaseZap,
      getValue: (stat) => stat?.cacheTokens ?? 0,
    },
    {
      key: 'cacheHitRate',
      title: t('Hit Rate'),
      description: t('Prompt cache hit rate'),
      icon: Percent,
      getValue: (stat) => stat?.cacheHitRate ?? 0,
    },
  ]
}

export function useSummaryCardsConfig(totals: {
  historicalTokensDisplay: string
  historicalInputTokensDisplay: string
  historicalOutputTokensDisplay: string
  historicalCacheTokensDisplay: string
  historicalCacheHitRateDisplay: string
  usedDisplay: string
  requestCountDisplay: string
  currencyLabel: string
  currencyEnabled: boolean
}) {
  const { t } = useTranslation()

  return [
    {
      key: 'historicalTokens',
      title: t('Tokens since launch'),
      value: totals.historicalTokensDisplay,
      description: t('Total Tokens'),
      icon: Layers,
      details: [
        {
          label: t('Input Tokens'),
          value: totals.historicalInputTokensDisplay,
        },
        {
          label: t('Output Tokens'),
          value: totals.historicalOutputTokensDisplay,
        },
        {
          label: t('Cache Hit Tokens'),
          value: totals.historicalCacheTokensDisplay,
        },
        {
          label: t('Hit Rate'),
          value: totals.historicalCacheHitRateDisplay,
        },
      ],
    },
    {
      key: 'usage',
      title: t('Historical Usage'),
      value: totals.usedDisplay,
      description: totals.currencyEnabled
        ? `${t('Total consumed')} (${totals.currencyLabel})`
        : t('Total consumed quota'),
      icon: TrendingUp,
      details: undefined,
    },
    {
      key: 'requests',
      title: t('Request Count'),
      value: totals.requestCountDisplay,
      description: t('Total requests made'),
      icon: Activity,
      details: undefined,
    },
  ]
}
