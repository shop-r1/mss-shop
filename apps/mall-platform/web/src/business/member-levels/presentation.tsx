import { useIntl } from '@umijs/max';
import { Tag } from 'antd';
import type { MemberLevelStatus } from './types';

export function memberLevelStatusPresentation(status: MemberLevelStatus) {
  return {
    color: status === 'enabled' ? 'green' : status === 'disabled' ? 'default' : 'error',
    messageKey: `memberLevels.values.status.${status}`,
  } as const;
}

export function memberLevelDefaultPresentation(isDefault: boolean) {
  return {
    color: isDefault ? 'blue' : 'default',
    messageKey: `memberLevels.values.default.${isDefault ? 'yes' : 'no'}`,
  } as const;
}

export function MemberLevelStatusTag({ status }: { status: MemberLevelStatus }) {
  const intl = useIntl();
  const presentation = memberLevelStatusPresentation(status);
  return (
    <Tag color={presentation.color}>{intl.formatMessage({ id: presentation.messageKey })}</Tag>
  );
}

export function MemberLevelDefaultTag({ isDefault }: { isDefault: boolean }) {
  const intl = useIntl();
  const presentation = memberLevelDefaultPresentation(isDefault);
  return (
    <Tag color={presentation.color}>{intl.formatMessage({ id: presentation.messageKey })}</Tag>
  );
}

export function formatMemberLevelDate(value: string, locale: string): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat(locale || 'zh-CN', {
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(new Date(value));
}
