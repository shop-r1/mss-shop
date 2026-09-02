import { getRequestErrorMessage } from '@mss-boot-io/admin-web/runtime';

type MessageParameter = boolean | number | string;
type MessageParameters = Record<string, MessageParameter>;

export type LegacyErrorTranslator = (id: string, params?: MessageParameters) => string;

const MESSAGE_KEYS = new Set([
  'legacy.errors.authenticationRequired',
  'legacy.errors.forbidden',
  'legacy.errors.authorizationUnavailable',
  'legacy.errors.resourceNotFound',
  'legacy.errors.recordNotFound',
  'legacy.errors.conflict',
  'legacy.errors.validationFailed',
  'legacy.errors.schemaNotReady',
  'legacy.errors.operationNotSupported',
  'legacy.errors.invalidRequest',
  'legacy.errors.internal',
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function nonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function errorBody(error: unknown): Record<string, unknown> | undefined {
  if (!isRecord(error)) return undefined;
  if (isRecord(error.data)) return error.data;
  if (isRecord(error.response) && isRecord(error.response.data)) {
    return error.response.data;
  }
  return error;
}

function safeParameters(value: unknown): MessageParameters | undefined {
  if (!isRecord(value)) return undefined;
  const result: MessageParameters = {};
  for (const [key, candidate] of Object.entries(value)) {
    if (
      typeof candidate === 'string' ||
      typeof candidate === 'number' ||
      typeof candidate === 'boolean'
    ) {
      result[key] = candidate;
    }
  }
  return Object.keys(result).length > 0 ? result : undefined;
}

function translatedMessage(
  translate: LegacyErrorTranslator,
  id: string,
  params?: MessageParameters,
): string | undefined {
  try {
    return nonEmptyString(translate(id, params));
  } catch {
    return undefined;
  }
}

function localizedEnvelopeMessage(
  body: Record<string, unknown> | undefined,
  translate: LegacyErrorTranslator,
): string | undefined {
  if (!body) return undefined;
  const topLevelKey = nonEmptyString(body.messageKey);
  if (topLevelKey && MESSAGE_KEYS.has(topLevelKey)) {
    return translatedMessage(translate, topLevelKey, safeParameters(body.params));
  }

  // Accept the previous envelope during rollout, but never pass its object to
  // getRequestErrorMessage or React.
  if (isRecord(body.error)) {
    const nestedKey = nonEmptyString(body.error.messageKey);
    if (nestedKey && MESSAGE_KEYS.has(nestedKey)) {
      return translatedMessage(translate, nestedKey, safeParameters(body.error.params));
    }
  }
  return undefined;
}

/** Resolve every backend or transport failure to a React-safe localized string. */
export function localizeLegacyError(error: unknown, translate: LegacyErrorTranslator): string {
  const body = errorBody(error);
  const localized = localizedEnvelopeMessage(body, translate);
  if (localized) return localized;

  const explicitMessage =
    nonEmptyString(body?.errorMessage) ??
    nonEmptyString(body?.message) ??
    (isRecord(body?.error)
      ? (nonEmptyString(body.error.errorMessage) ?? nonEmptyString(body.error.message))
      : undefined);
  if (explicitMessage) return explicitMessage;

  try {
    const fallback = nonEmptyString(getRequestErrorMessage(error));
    if (fallback && fallback !== 'Request failed') return fallback;
  } catch {
    // The shared helper is typed as string but older/nested envelopes can make
    // it observe malformed values. The stable generic key remains safe.
  }
  return translatedMessage(translate, 'legacy.errors.requestFailed') ?? 'Request failed';
}
