export interface GroupModelMappingRow {
  source: string
  target: string
}

export function groupModelMappingToRows(mapping?: Record<string, string>): GroupModelMappingRow[] {
  return Object.entries(mapping || {}).map(([source, target]) => ({ source, target }))
}

export function groupModelMappingToAPI(rows: GroupModelMappingRow[]): Record<string, string> {
  const entries = rows.map(({ source, target }) => [source.trim(), target.trim()] as const)
  if (entries.some(([source, target]) => !source || !target || source.slice(0, -1).includes('*') || target.includes('*')) ||
      new Set(entries.map(([source]) => source)).size !== entries.length) {
    throw new Error('Invalid model mapping: use unique source models, non-empty targets, and only a trailing * in sources.')
  }
  return Object.fromEntries(entries)
}
