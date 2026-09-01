import { describe, expect, it } from 'vitest';
import {
  formatMemberLevelDate,
  memberLevelDefaultPresentation,
  memberLevelStatusPresentation,
} from './presentation';

describe('member-level presentation helpers', () => {
  it('maps every typed status and default state to a stable locale key and visual token', () => {
    expect(memberLevelStatusPresentation('enabled')).toEqual({
      color: 'green',
      messageKey: 'memberLevels.values.status.enabled',
    });
    expect(memberLevelStatusPresentation('disabled')).toEqual({
      color: 'default',
      messageKey: 'memberLevels.values.status.disabled',
    });
    expect(memberLevelStatusPresentation('unknown')).toEqual({
      color: 'error',
      messageKey: 'memberLevels.values.status.unknown',
    });
    expect(memberLevelDefaultPresentation(true)).toEqual({
      color: 'blue',
      messageKey: 'memberLevels.values.default.yes',
    });
    expect(memberLevelDefaultPresentation(false)).toEqual({
      color: 'default',
      messageKey: 'memberLevels.values.default.no',
    });
  });

  it('uses an explicit empty placeholder and rejects invalid timestamps', () => {
    expect(formatMemberLevelDate('', 'en-US')).toBe('—');
    expect(formatMemberLevelDate('2026-09-01T00:00:00Z', 'en-US')).not.toBe('—');
    expect(() => formatMemberLevelDate('not-a-timestamp', 'en-US')).toThrow(RangeError);
  });
});
