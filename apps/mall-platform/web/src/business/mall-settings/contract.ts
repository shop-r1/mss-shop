import { hasPermission, type CurrentUser } from '@mss-boot-io/admin-web/runtime';
import type {
  MallGeneralSettings,
  MallGeneralSettingsCapabilities,
  MallGeneralSettingsFieldDefinition,
  MallGeneralSettingsInput,
} from './types';
import { mallGeneralSettingsPermissionPaths } from './paths';

export const mallGeneralSettingsMaxBytes = {
  mallName: 256,
  orderPrefix: 64,
  defaultSenderName: 256,
  defaultSenderPhone: 64,
} as const;

/**
 * This is the complete UI allow-list. New backend keys are not displayed or
 * submitted until they are deliberately classified as safe general settings.
 */
export const mallGeneralSettingsFields = [
  {
    name: 'mallName',
    labelMessageId: 'mallSettings.general.fields.mallName',
    helpMessageId: 'mallSettings.general.fields.mallNameHelp',
    placeholderMessageId: 'mallSettings.general.fields.mallNamePlaceholder',
    defaultLabel: 'Mall name',
    defaultHelp: 'The customer-facing name of this mall.',
    defaultPlaceholder: 'Enter the mall name',
    maxBytes: mallGeneralSettingsMaxBytes.mallName,
    inputType: 'text',
    autoComplete: 'organization',
  },
  {
    name: 'orderPrefix',
    labelMessageId: 'mallSettings.general.fields.orderPrefix',
    helpMessageId: 'mallSettings.general.fields.orderPrefixHelp',
    placeholderMessageId: 'mallSettings.general.fields.orderPrefixPlaceholder',
    defaultLabel: 'Order prefix',
    defaultHelp:
      'Preserves the legacy ewePrefix value for later order-workflow restoration; it does not change order or shipping-label numbers yet.',
    defaultPlaceholder: 'Enter an order prefix',
    maxBytes: mallGeneralSettingsMaxBytes.orderPrefix,
    inputType: 'text',
    autoComplete: 'off',
  },
  {
    name: 'defaultSenderName',
    labelMessageId: 'mallSettings.general.fields.defaultSenderName',
    helpMessageId: 'mallSettings.general.fields.defaultSenderNameHelp',
    placeholderMessageId: 'mallSettings.general.fields.defaultSenderNamePlaceholder',
    defaultLabel: 'Default sender',
    defaultHelp:
      'Reserved as the fallback sender for the future fulfillment workflow; saving it does not call a courier or change historical orders.',
    defaultPlaceholder: 'Enter the default sender name',
    maxBytes: mallGeneralSettingsMaxBytes.defaultSenderName,
    inputType: 'text',
    autoComplete: 'name',
  },
  {
    name: 'defaultSenderPhone',
    labelMessageId: 'mallSettings.general.fields.defaultSenderPhone',
    helpMessageId: 'mallSettings.general.fields.defaultSenderPhoneHelp',
    placeholderMessageId: 'mallSettings.general.fields.defaultSenderPhonePlaceholder',
    defaultLabel: 'Default sender phone',
    defaultHelp: 'The fallback phone number retained with the default sender.',
    defaultPlaceholder: 'Enter the default sender phone number',
    maxBytes: mallGeneralSettingsMaxBytes.defaultSenderPhone,
    inputType: 'tel',
    autoComplete: 'tel',
  },
] as const satisfies readonly MallGeneralSettingsFieldDefinition[];

const responseKeys = mallGeneralSettingsFields.map((field) => field.name);
const responseKeySet = new Set<string>(responseKeys);

interface MallGeneralSettingsCandidate {
  mallName?: unknown;
  orderPrefix?: unknown;
  defaultSenderName?: unknown;
  defaultSenderPhone?: unknown;
}

export class MallGeneralSettingsContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MallGeneralSettingsContractError';
  }
}

function candidateObject(value: unknown): MallGeneralSettingsCandidate {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new MallGeneralSettingsContractError('Mall general settings must be an object');
  }
  const keys = Object.keys(value);
  if (
    keys.length !== responseKeys.length ||
    keys.some((key) => !responseKeySet.has(key))
  ) {
    throw new MallGeneralSettingsContractError(
      'Mall general settings contain missing or unsupported fields',
    );
  }
  return value as MallGeneralSettingsCandidate;
}

function stringField(value: unknown, key: string, maxBytes: number): string {
  if (typeof value !== 'string' || utf8ByteLength(value) > maxBytes) {
    throw new MallGeneralSettingsContractError(`Mall general settings field ${key} is invalid`);
  }
  return value;
}

export function parseMallGeneralSettings(value: unknown): MallGeneralSettings {
  const candidate = candidateObject(value);
  return {
    mallName: stringField(
      candidate.mallName,
      'mallName',
      mallGeneralSettingsMaxBytes.mallName,
    ),
    orderPrefix: stringField(
      candidate.orderPrefix,
      'orderPrefix',
      mallGeneralSettingsMaxBytes.orderPrefix,
    ),
    defaultSenderName: stringField(
      candidate.defaultSenderName,
      'defaultSenderName',
      mallGeneralSettingsMaxBytes.defaultSenderName,
    ),
    defaultSenderPhone: stringField(
      candidate.defaultSenderPhone,
      'defaultSenderPhone',
      mallGeneralSettingsMaxBytes.defaultSenderPhone,
    ),
  };
}

export function mallGeneralSettingsInput(
  value: MallGeneralSettingsInput,
): MallGeneralSettingsInput {
  return {
    mallName: value.mallName,
    orderPrefix: value.orderPrefix,
    defaultSenderName: value.defaultSenderName,
    defaultSenderPhone: value.defaultSenderPhone,
  };
}

export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function emptyMallGeneralSettings(): MallGeneralSettings {
  return {
    mallName: '',
    orderPrefix: '',
    defaultSenderName: '',
    defaultSenderPhone: '',
  };
}

export function isMallGeneralSettingsEmpty(value: MallGeneralSettings): boolean {
  return responseKeys.every((key) => value[key] === '');
}

export function getMallGeneralSettingsCapabilities(
  user?: CurrentUser,
): MallGeneralSettingsCapabilities {
  const canAccessRoute = hasPermission(user, mallGeneralSettingsPermissionPaths.route);
  return {
    canRead: canAccessRoute && hasPermission(user, mallGeneralSettingsPermissionPaths.read),
    canUpdate: canAccessRoute && hasPermission(user, mallGeneralSettingsPermissionPaths.update),
  };
}
