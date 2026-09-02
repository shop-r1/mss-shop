import { getRequestErrorMessage } from '@mss-boot-io/admin-web/runtime';
import type { MemberLevelReferenceCounts } from './types';

export interface MemberLevelErrorParameters
  extends Record<string, string | undefined>,
    Partial<MemberLevelReferenceCounts> {
  field?: string;
  id?: string;
  rule?: string;
}

export type MemberLevelErrorTranslator = (
  id: string,
  parameters?: MemberLevelErrorParameters,
) => string | undefined;

interface ErrorCandidate {
  data?: unknown;
  response?: unknown;
  name?: unknown;
  messageKey?: unknown;
  params?: unknown;
  errorMessage?: unknown;
  message?: unknown;
}

interface ErrorParameterCandidate {
  count?: unknown;
  members?: unknown;
  activities?: unknown;
  couponTemplates?: unknown;
  goodsPrices?: unknown;
  field?: unknown;
  id?: unknown;
  rule?: unknown;
  references?: unknown;
}

const supportedMessageKeys = new Set([
  'memberLevels.errors.authenticationRequired',
  'memberLevels.errors.forbidden',
  'memberLevels.errors.authorizationUnavailable',
  'memberLevels.errors.invalidRequest',
  'memberLevels.errors.validationFailed',
  'memberLevels.errors.notFound',
  'memberLevels.errors.duplicateName',
  'memberLevels.errors.conflict',
  'memberLevels.errors.revisionConflict',
  'memberLevels.errors.defaultRequired',
  'memberLevels.errors.defaultRepairRequired',
  'memberLevels.errors.defaultProtected',
  'memberLevels.errors.memberReferences',
  'memberLevels.errors.paymentPolicySourceRequired',
  'memberLevels.errors.paymentPolicySourceInvalid',
  'memberLevels.errors.paymentPolicySource',
  'memberLevels.errors.inUse',
  'memberLevels.errors.legacyDataIncompatible',
  'memberLevels.errors.schemaNotReady',
  'memberLevels.errors.mutationDisabled',
  'memberLevels.errors.internal',
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

function scalarString(value: unknown): string | undefined {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  if (typeof value === 'boolean') return String(value);
  return undefined;
}

function errorParameters(value: unknown): MemberLevelErrorParameters | undefined {
  const parsed = parameterCandidate(value);
  if (!parsed) return undefined;
  const legacyReferences = parameterCandidate(parsed.references);
  const parameters: MemberLevelErrorParameters = {
    count: scalarString(parsed.count),
    members: scalarString(parsed.members) ?? scalarString(legacyReferences?.members),
    activities: scalarString(parsed.activities) ?? scalarString(legacyReferences?.activities),
    couponTemplates:
      scalarString(parsed.couponTemplates) ?? scalarString(legacyReferences?.couponTemplates),
    goodsPrices: scalarString(parsed.goodsPrices) ?? scalarString(legacyReferences?.goodsPrices),
    field: scalarString(parsed.field),
    id: scalarString(parsed.id),
    rule: scalarString(parsed.rule),
  };
  return parameters.count ||
    parameters.members ||
    parameters.activities ||
    parameters.couponTemplates ||
    parameters.goodsPrices ||
    parameters.field ||
    parameters.id ||
    parameters.rule
    ? parameters
    : undefined;
}

export function localizeMemberLevelError(
  error: unknown,
  translate: MemberLevelErrorTranslator,
  fallback: string,
): string {
  const body = errorBody(error);
  const messageKey = nonEmptyString(body?.messageKey);
  if (messageKey && supportedMessageKeys.has(messageKey)) {
    const translated = translate(messageKey, errorParameters(body?.params));
    if (translated && translated !== messageKey) return translated;
  }

  const contractFailure = body?.name === 'MemberLevelContractError';
  const explicitMessage = contractFailure
    ? undefined
    : (nonEmptyString(body?.errorMessage) ?? nonEmptyString(body?.message));
  if (explicitMessage) return explicitMessage;

  try {
    const transportMessage = nonEmptyString(getRequestErrorMessage(error));
    if (transportMessage && transportMessage !== 'Request failed') return transportMessage;
  } catch {
    // Keep the stable, localized caller fallback for malformed transport data.
  }
  return fallback;
}
