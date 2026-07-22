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
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'

const apiRestrictSchema = z.object({
  ApiRestrictEnabled: z.boolean(),
  ApiRestrictMessage: z.string(),
  ApiRestrictUserIds: z.string(),
})

type ApiRestrictFormValues = z.infer<typeof apiRestrictSchema>

type ApiRestrictSectionProps = {
  defaultValues: ApiRestrictFormValues
}

export function ApiRestrictSection({ defaultValues }: ApiRestrictSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<ApiRestrictFormValues>({
    resolver: zodResolver(apiRestrictSchema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: ApiRestrictFormValues) => {
    const next = {
      ApiRestrictEnabled: values.ApiRestrictEnabled,
      ApiRestrictMessage: values.ApiRestrictMessage,
      ApiRestrictUserIds: values.ApiRestrictUserIds.trim(),
    }

    const updates: Array<{ key: string; value: string | boolean }> = []
    if (next.ApiRestrictMessage !== defaultValues.ApiRestrictMessage) {
      updates.push({ key: 'ApiRestrictMessage', value: next.ApiRestrictMessage })
    }
    if (next.ApiRestrictUserIds !== defaultValues.ApiRestrictUserIds) {
      updates.push({ key: 'ApiRestrictUserIds', value: next.ApiRestrictUserIds })
    }
    // Toggle the master switch last so message/user list are already saved.
    if (next.ApiRestrictEnabled !== defaultValues.ApiRestrictEnabled) {
      updates.push({ key: 'ApiRestrictEnabled', value: next.ApiRestrictEnabled })
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
  }

  return (
    <SettingsSection title={t('API Access Restriction')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='ApiRestrictEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable API access restriction')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Restricted users can still log in, but all of their API key requests return the custom message below.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='ApiRestrictMessage'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Custom message')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={3}
                    placeholder={t('Service is under maintenance, please try again later.')}
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t('This message is returned for every restricted API key request.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='ApiRestrictUserIds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Restricted user IDs')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('e.g. 12,34,56')}
                    autoComplete='off'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Comma-separated user IDs to restrict. Leave empty to restrict ALL users (global maintenance mode).'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
