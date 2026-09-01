import { describe, expect, it } from 'vitest';
import {
  buildMemberLevelQuery,
  createMemberLevelInput,
  intersectMemberLevelOperations,
  MemberLevelContractError,
  parseMemberLevel,
  parseMemberLevelPage,
} from './contract';
import type { MemberLevelCapabilities } from './types';

const revision = 'a'.repeat(64);

function recordFixture() {
  return {
    id: '100000000000000001',
    name: 'Standard',
    discountPercent: '10.5',
    status: 'enabled',
    isDefault: true,
    createdAt: '2026-09-01T00:00:00Z',
    updatedAt: '2026-09-01T00:00:00Z',
    revision,
  };
}

function pageFixture() {
  return {
    data: [recordFixture()],
    total: 1,
    current: 1,
    pageSize: 20,
    integrity: {
      flaggedDefaultCount: 1,
      enabledDefaultCount: 1,
      invalidDefaultCount: 0,
    },
    operations: {
      create: false,
      update: false,
      setDefault: false,
      delete: false,
    },
  };
}

describe('member-level transport contract', () => {
  it('accepts only the backend 64-character lowercase hexadecimal revision', () => {
    expect(parseMemberLevel(recordFixture()).revision).toBe(revision);

    for (const invalidRevision of [
      '',
      'a'.repeat(63),
      'a'.repeat(65),
      'A'.repeat(64),
      'g'.repeat(64),
    ]) {
      expect(() => parseMemberLevel({ ...recordFixture(), revision: invalidRevision })).toThrow(
        MemberLevelContractError,
      );
    }
  });

  it('requires canonical discounts in responses while accepting normalizable editor input', () => {
    expect(parseMemberLevel(recordFixture()).discountPercent).toBe('10.5');
    for (const nonCanonical of ['0.0', '10.00', '10.50', '100.00']) {
      expect(() => parseMemberLevel({ ...recordFixture(), discountPercent: nonCanonical })).toThrow(
        MemberLevelContractError,
      );
    }

    expect(
      createMemberLevelInput({
        name: 'Wholesale',
        discountPercent: '10.50',
        status: 'enabled',
      }).discountPercent,
    ).toBe('10.50');
  });

  it('requires an exact page, integrity, and server-operation shape', () => {
    expect(parseMemberLevelPage(pageFixture(), { current: 1, pageSize: 20 })).toEqual(
      pageFixture(),
    );

    expect(() =>
      parseMemberLevelPage(
        { ...pageFixture(), operations: { ...pageFixture().operations, delete: 'false' } },
        { current: 1, pageSize: 20 },
      ),
    ).toThrow(MemberLevelContractError);
    expect(() =>
      parseMemberLevelPage(
        {
          ...pageFixture(),
          integrity: {
            flaggedDefaultCount: 1,
            enabledDefaultCount: 1,
            invalidDefaultCount: 1,
          },
        },
        { current: 1, pageSize: 20 },
      ),
    ).toThrow(MemberLevelContractError);
    const missingOperations = pageFixture() as Record<string, unknown>;
    delete missingOperations.operations;
    expect(() => parseMemberLevelPage(missingOperations, { current: 1, pageSize: 20 })).toThrow(
      MemberLevelContractError,
    );
    expect(() =>
      parseMemberLevelPage(
        { ...pageFixture(), data: [{ ...recordFixture(), payment_ids: ['hidden'] }] },
        { current: 1, pageSize: 20 },
      ),
    ).toThrow(MemberLevelContractError);
  });

  it('fails pagination overflow and intersects permissions with fail-closed operations', () => {
    expect(() => buildMemberLevelQuery(Number.MAX_SAFE_INTEGER, 100, {})).toThrow(
      MemberLevelContractError,
    );
    expect(buildMemberLevelQuery(2, 50, { q: ' Standard ', isDefault: 'false' })).toEqual({
      current: 2,
      pageSize: 50,
      q: 'Standard',
      isDefault: false,
    });

    const capabilities: MemberLevelCapabilities = {
      canList: true,
      canRead: true,
      canCreate: true,
      canUpdate: true,
      canSetDefault: true,
      canDelete: true,
    };
    expect(intersectMemberLevelOperations(capabilities)).toEqual({
      ...capabilities,
      canCreate: false,
      canUpdate: false,
      canSetDefault: false,
      canDelete: false,
    });
    expect(
      intersectMemberLevelOperations(capabilities, {
        create: true,
        update: false,
        setDefault: true,
        delete: false,
      }),
    ).toEqual({
      ...capabilities,
      canCreate: true,
      canUpdate: false,
      canSetDefault: true,
      canDelete: false,
    });
  });
});
