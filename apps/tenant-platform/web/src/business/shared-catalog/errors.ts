import { getRequestErrorMessage } from '@mss-boot-io/admin-web/runtime';

type MessageParameter = boolean | number | string;
type MessageParameters = Record<string, MessageParameter>;

export type SharedCatalogErrorTranslator = (id: string, params?: MessageParameters) => string;

const MESSAGE_KEY_MAP: Readonly<Record<string, string>> = {
  'legacy.errors.authenticationRequired': 'sharedCatalog.errors.authenticationRequired',
  'legacy.errors.forbidden': 'sharedCatalog.errors.forbidden',
  'legacy.errors.authorizationUnavailable': 'sharedCatalog.errors.authorizationUnavailable',
  'legacy.errors.resourceNotFound': 'sharedCatalog.errors.resourceNotFound',
  'legacy.errors.recordNotFound': 'sharedCatalog.errors.recordNotFound',
  'legacy.errors.conflict': 'sharedCatalog.errors.conflict',
  'legacy.errors.validationFailed': 'sharedCatalog.errors.validationFailed',
  'legacy.errors.schemaNotReady': 'sharedCatalog.errors.schemaNotReady',
  'legacy.errors.operationNotSupported': 'sharedCatalog.errors.operationNotSupported',
  'legacy.errors.invalidRequest': 'sharedCatalog.errors.invalidRequest',
  'legacy.errors.internal': 'sharedCatalog.errors.internal',
};

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
  translate: SharedCatalogErrorTranslator,
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
  translate: SharedCatalogErrorTranslator,
): string | undefined {
  if (!body) return undefined;
  const topLevelKey = nonEmptyString(body.messageKey);
  const topLevelMessageID = topLevelKey ? MESSAGE_KEY_MAP[topLevelKey] : undefined;
  if (topLevelMessageID) {
    return translatedMessage(translate, topLevelMessageID, safeParameters(body.params));
  }

  if (isRecord(body.error)) {
    const nestedKey = nonEmptyString(body.error.messageKey);
    const nestedMessageID = nestedKey ? MESSAGE_KEY_MAP[nestedKey] : undefined;
    if (nestedMessageID) {
      return translatedMessage(translate, nestedMessageID, safeParameters(body.error.params));
    }
  }
  return undefined;
}

/** Resolve every backend or transport failure to a React-safe localized string. */
export function localizeSharedCatalogError(
  error: unknown,
  translate: SharedCatalogErrorTranslator,
): string {
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
    // Malformed legacy envelopes must never escape as React children.
  }
  return translatedMessage(translate, 'sharedCatalog.errors.requestFailed') ?? 'Request failed';
}
