import { getRequestErrorMessage } from '@mss-boot-io/admin-web/runtime';

export interface MallSettingsErrorParameters {
  field?: string;
  rule?: string;
}

export type MallSettingsErrorTranslator = (
  id: string,
  parameters?: MallSettingsErrorParameters,
) => string | undefined;

interface ErrorCandidate {
  data?: unknown;
  response?: unknown;
  messageKey?: unknown;
  params?: unknown;
  errorMessage?: unknown;
  message?: unknown;
}

interface ErrorParameterCandidate {
  field?: unknown;
  rule?: unknown;
}

const supportedMessageKeys = new Set([
  'mallSettings.errors.authenticationRequired',
  'mallSettings.errors.forbidden',
  'mallSettings.errors.authorizationUnavailable',
  'mallSettings.errors.invalidRequest',
  'mallSettings.errors.validationFailed',
  'mallSettings.errors.conflict',
  'mallSettings.errors.legacyMetadataIncompatible',
  'mallSettings.errors.schemaNotReady',
  'mallSettings.errors.internal',
]);

function candidate(value: unknown): ErrorCandidate | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined;
  return value as ErrorCandidate;
}

function parameterCandidate(value: unknown): ErrorParameterCandidate | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined;
  return value as ErrorParameterCandidate;
}

function errorBody(error: unknown): ErrorCandidate | undefined {
  const direct = candidate(error);
  const response = candidate(direct?.response);
  return candidate(response?.data) ?? candidate(direct?.data) ?? direct;
}

function nonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function errorParameters(value: unknown): MallSettingsErrorParameters | undefined {
  const parsed = parameterCandidate(value);
  if (!parsed) return undefined;
  const field = nonEmptyString(parsed.field);
  const rule = nonEmptyString(parsed.rule);
  return field || rule ? { field, rule } : undefined;
}

export function localizeMallSettingsError(
  error: unknown,
  translate: MallSettingsErrorTranslator,
  fallback: string,
): string {
  const body = errorBody(error);
  const messageKey = nonEmptyString(body?.messageKey);
  if (messageKey && supportedMessageKeys.has(messageKey)) {
    const translated = translate(messageKey, errorParameters(body?.params));
    if (translated && translated !== messageKey) return translated;
  }

  const explicitMessage = nonEmptyString(body?.errorMessage) ?? nonEmptyString(body?.message);
  if (explicitMessage) return explicitMessage;

  try {
    const transportMessage = nonEmptyString(getRequestErrorMessage(error));
    if (transportMessage && transportMessage !== 'Request failed') return transportMessage;
  } catch {
    // The stable caller-provided fallback remains safe for malformed envelopes.
  }
  return fallback;
}
