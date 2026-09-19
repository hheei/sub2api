/**
 * User Groups API endpoints (non-admin)
 * Handles group-related operations for regular users
 */

import { apiClient } from './client'
import type { Group, UserGroupRateDisplay } from '@/types'

/**
 * Get available groups that the current user can bind to API keys
 * This returns groups based on user's permissions:
 * - Standard groups: public (non-exclusive) or explicitly allowed
 * - Subscription groups: user has active subscription
 * @returns List of available groups
 */
export async function getAvailable(): Promise<Group[]> {
  const { data } = await apiClient.get<Group[]>('/groups/available')
  return data
}

/**
 * Get current user's custom group rate multipliers.
 * 只返回生效数值与动态标记；动态倍率的数值仅为回退值，原始表达式从不下发。
 * @returns Map of group_id to {rate_multiplier, is_dynamic}
 */
export async function getUserGroupRates(): Promise<Record<number, UserGroupRateDisplay>> {
  const { data } = await apiClient.get<Record<number, UserGroupRateDisplay> | null>('/groups/rates')
  return data || {}
}

export const userGroupsAPI = {
  getAvailable,
  getUserGroupRates
}

export default userGroupsAPI
