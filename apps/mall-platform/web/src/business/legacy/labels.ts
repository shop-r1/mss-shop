import type { LegacyResourceColumn } from './types';

interface MessageDescriptor {
  id: string;
  defaultMessage: string;
}

export type LegacyFieldMessageFormatter = (descriptor: MessageDescriptor) => string;

const MESSAGE_KEY_PATTERN = /^legacy\.fields\.[a-z][a-z0-9_]*$/;
const ACRONYMS = new Set([
  'api',
  'aud',
  'cny',
  'csv',
  'erp',
  'html',
  'id',
  'ids',
  'ip',
  'json',
  'qr',
  'rmb',
  'sku',
  'url',
]);

export function humanizeLegacyFieldName(name: string): string {
  const words = name
    .trim()
    .split(/_+/)
    .filter(Boolean)
    .map((word) =>
      ACRONYMS.has(word.toLowerCase())
        ? word.toUpperCase()
        : word.charAt(0).toUpperCase() + word.slice(1).toLowerCase(),
    );
  return words.join(' ') || 'Field';
}

/**
 * Resolve the stable message key supplied by the backend descriptor. Unknown
 * future fields deliberately fall back to their humanized field name so a raw
 * message key can never reach a table, detail panel, form or validation error.
 */
export function localizeLegacyFieldLabel(
  column: LegacyResourceColumn,
  formatMessage: LegacyFieldMessageFormatter,
): string {
  const fallback = humanizeLegacyFieldName(column.name);
  if (!MESSAGE_KEY_PATTERN.test(column.label)) return fallback;

  try {
    const localized = formatMessage({
      id: column.label,
      defaultMessage: fallback,
    }).trim();
    if (!localized || localized === column.label || localized.startsWith('legacy.fields.')) {
      return fallback;
    }
    return localized;
  } catch {
    return fallback;
  }
}
