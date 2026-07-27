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
  SORT_OPTIONS,
  FILTER_ALL,
  QUOTA_TYPES,
  QUOTA_TYPE_VALUES,
  ENDPOINT_TYPES,
} from '../constants'
import type { PricingModel } from '../types'

// ----------------------------------------------------------------------------
// Filter Utilities
// ----------------------------------------------------------------------------

/**
 * Filter models by search query
 */
export function filterBySearch(
  models: PricingModel[],
  query: string
): PricingModel[] {
  if (!query) return models

  const lowerQuery = query.toLowerCase()
  return models.filter(
    (m) =>
      m.model_name?.toLowerCase().includes(lowerQuery) ||
      m.description?.toLowerCase().includes(lowerQuery) ||
      m.tags?.toLowerCase().includes(lowerQuery) ||
      m.vendor_name?.toLowerCase().includes(lowerQuery)
  )
}

/**
 * Filter models by vendor
 */
export function filterByVendor(
  models: PricingModel[],
  vendor: string
): PricingModel[] {
  if (vendor === FILTER_ALL) return models
  return models.filter((m) => m.vendor_name === vendor)
}

/**
 * Filter models by group
 */
export function filterByGroup(
  models: PricingModel[],
  group: string
): PricingModel[] {
  if (group === FILTER_ALL) return models
  return models.filter((m) => m.enable_groups?.includes(group))
}

/**
 * Filter models by quota type
 */
export function filterByQuotaType(
  models: PricingModel[],
  quotaType: string
): PricingModel[] {
  if (quotaType === QUOTA_TYPES.ALL) return models
  const targetType =
    quotaType === QUOTA_TYPES.TOKEN
      ? QUOTA_TYPE_VALUES.TOKEN
      : QUOTA_TYPE_VALUES.REQUEST
  return models.filter((m) => m.quota_type === targetType)
}

/**
 * Filter models by endpoint type
 */
export function filterByEndpointType(
  models: PricingModel[],
  endpointType: string
): PricingModel[] {
  if (endpointType === ENDPOINT_TYPES.ALL) return models
  return models.filter((m) =>
    m.supported_endpoint_types?.includes(endpointType)
  )
}

/**
 * Move the active filter value to the front of a display list.
 *
 * Cards and table cells truncate these lists (groups to 1, endpoints/tags to 2),
 * so without this the value the user filtered by is often hidden behind "+N".
 * Returns the list unchanged when nothing is selected or no item matches.
 */
export function prioritizeSelected(
  items: string[],
  selected?: string,
  options: { caseInsensitive?: boolean } = {}
): string[] {
  if (!selected || selected === FILTER_ALL) return items

  const target = options.caseInsensitive ? selected.toLowerCase() : selected
  const index = items.findIndex((item) =>
    options.caseInsensitive ? item.toLowerCase() === target : item === target
  )
  if (index <= 0) return items

  return [items[index], ...items.slice(0, index), ...items.slice(index + 1)]
}

/**
 * Get model price for sorting
 */
function getModelPrice(model: PricingModel): number {
  return model.quota_type === 0 ? model.model_ratio : model.model_price || 0
}

/**
 * Sort models by specified option
 */
export function sortModels(
  models: PricingModel[],
  sortBy: string
): PricingModel[] {
  const sorted = [...models]

  switch (sortBy) {
    case SORT_OPTIONS.NAME:
      sorted.sort((a, b) =>
        (a.model_name || '').localeCompare(b.model_name || '')
      )
      break
    case SORT_OPTIONS.PRICE_LOW:
      sorted.sort((a, b) => getModelPrice(a) - getModelPrice(b))
      break
    case SORT_OPTIONS.PRICE_HIGH:
      sorted.sort((a, b) => getModelPrice(b) - getModelPrice(a))
      break
  }

  return sorted
}

/**
 * Apply all filters and sorting to models
 */
export function filterAndSortModels(
  models: PricingModel[],
  filters: {
    search: string
    vendor: string
    group: string
    quotaType: string
    endpointType: string
    tag: string
    sortBy: string
  }
): PricingModel[] {
  let result = filterBySearch(models, filters.search)
  result = filterByVendor(result, filters.vendor)
  result = filterByGroup(result, filters.group)
  result = filterByQuotaType(result, filters.quotaType)
  result = filterByEndpointType(result, filters.endpointType)
  result = filterByTag(result, filters.tag)
  result = sortModels(result, filters.sortBy)

  return result
}

/**
 * Parse tags from comma-separated string
 */
export function parseTags(tagsString?: string): string[] {
  if (!tagsString) return []
  return tagsString
    .split(/[,;|\s]+/)
    .map((t) => t.trim())
    .filter(Boolean)
}

/**
 * Extract all unique tags from models
 */
export function extractAllTags(models: PricingModel[]): string[] {
  const tagSet = new Set<string>()

  models.forEach((model) => {
    if (model.tags) {
      const tags = parseTags(model.tags)
      tags.forEach((tag) => {
        tagSet.add(tag.toLowerCase())
      })
    }
  })

  return Array.from(tagSet).sort((a, b) => a.localeCompare(b))
}

/**
 * Filter models by tag
 */
export function filterByTag(
  models: PricingModel[],
  tag: string
): PricingModel[] {
  if (tag === FILTER_ALL) return models

  const tagLower = tag.toLowerCase()
  return models.filter((m) => {
    if (!m.tags) return false
    const modelTags = parseTags(m.tags).map((t) => t.toLowerCase())
    return modelTags.includes(tagLower)
  })
}
