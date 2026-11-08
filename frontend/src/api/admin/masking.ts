/**
 * Admin API: global outbound masking (masking_config)
 */

import { apiClient } from '../client'

export type MaskingMode = 'rules' | 'rules+tagger'
export type MaskingAction = 'mask' | 'warn' | 'off'

export interface MaskingTaggerConfig {
  base_url: string
  model: string
  api_key?: string
  timeout_ms: number
  max_leaves: number
}

export interface MaskingNoticeConfig {
  enabled?: boolean | null
  text?: string
}

export interface MaskingConfig {
  enabled: boolean
  mode: MaskingMode
  actions: Record<string, MaskingAction>
  allow: string[]
  max_body_bytes: number
  tagger: MaskingTaggerConfig
  notice?: MaskingNoticeConfig | null
}

export interface MaskingConfigView {
  config: MaskingConfig
  has_api_key: boolean
  default_notice: string
  kinds: string[]
}

export interface MaskingPreviewResult {
  masked: string
  hits: number
  kinds: string[]
}

const basePath = '/admin/settings/masking'

export async function getConfig(): Promise<MaskingConfigView> {
  const { data } = await apiClient.get<MaskingConfigView>(`${basePath}/config`)
  return data
}

export async function updateConfig(config: MaskingConfig): Promise<MaskingConfigView> {
  const { data } = await apiClient.put<MaskingConfigView>(`${basePath}/config`, { config })
  return data
}

export async function preview(text: string, config?: MaskingConfig): Promise<MaskingPreviewResult> {
  const { data } = await apiClient.post<MaskingPreviewResult>(`${basePath}/preview`, { text, config })
  return data
}

export const maskingAPI = { getConfig, updateConfig, preview }

export default maskingAPI
