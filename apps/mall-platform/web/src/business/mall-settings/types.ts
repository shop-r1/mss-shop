export interface MallGeneralSettingsInput {
  mallName: string;
  orderPrefix: string;
  defaultSenderName: string;
  defaultSenderPhone: string;
}

export interface MallGeneralSettingsOperations {
  update: boolean;
}

export interface MallGeneralSettings extends MallGeneralSettingsInput {
  operations: MallGeneralSettingsOperations;
}

export type MallGeneralSettingsFieldName = keyof MallGeneralSettingsInput;

export interface MallGeneralSettingsFieldDefinition {
  name: MallGeneralSettingsFieldName;
  labelMessageId: string;
  helpMessageId: string;
  placeholderMessageId: string;
  defaultLabel: string;
  defaultHelp: string;
  defaultPlaceholder: string;
  maxBytes: number;
  inputType: 'tel' | 'text';
  autoComplete: string;
}

export interface MallGeneralSettingsCapabilities {
  canRead: boolean;
  canUpdate: boolean;
}
