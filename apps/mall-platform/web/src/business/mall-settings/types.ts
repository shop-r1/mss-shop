export interface MallGeneralSettings {
  mallName: string;
  orderPrefix: string;
  defaultSenderName: string;
  defaultSenderPhone: string;
}

export type MallGeneralSettingsInput = MallGeneralSettings;

export type MallGeneralSettingsFieldName = keyof MallGeneralSettings;

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
