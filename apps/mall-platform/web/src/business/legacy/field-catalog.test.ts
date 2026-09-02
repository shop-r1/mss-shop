import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { LEGACY_FIELDS } from './catalog';

const manifestPath = resolve(process.cwd(), '../internal/platform/legacydb/manifest.go');

function backendVisibleFields(): string[] {
  const source = readFileSync(manifestPath, 'utf8');
  const seeds = source
    .split('\n')
    .filter((line) => line.includes('{name:') && line.includes('columns:'));

  expect(seeds).toHaveLength(50);
  expect(source).toContain('Label:    "legacy.fields." + name');

  const visible = new Set<string>();
  for (const seed of seeds) {
    const columns = seed
      .match(/\bcolumns:\s*"([^"]+)"/)?.[1]
      ?.split(/\s+/)
      .filter(Boolean);
    if (!columns) throw new Error(`backend seed is missing columns: ${seed}`);
    const secrets = new Set(
      seed
        .match(/\bsecrets:\s*"([^"]+)"/)?.[1]
        ?.split(/\s+/)
        .filter(Boolean) ?? [],
    );
    for (const column of columns) {
      if (!secrets.has(column)) visible.add(column);
    }
  }
  return [...visible].sort();
}

describe('mall legacy field locale contract', () => {
  it('matches every visible field in the 50-resource backend manifest', () => {
    expect([...LEGACY_FIELDS]).toEqual([...LEGACY_FIELDS].sort());
    expect(new Set(LEGACY_FIELDS).size).toBe(LEGACY_FIELDS.length);
    expect([...LEGACY_FIELDS]).toEqual(backendVisibleFields());
  });

  it('provides non-key Simplified Chinese and English labels for every field', () => {
    for (const field of LEGACY_FIELDS) {
      const key = `legacy.fields.${field}`;
      for (const messages of [zhCN, enUS]) {
        const label = messages[key];
        expect(label, `${key} must be localized`).toBeTruthy();
        expect(label).not.toBe(key);
        expect(label).not.toMatch(/^legacy\.fields\./);
      }
    }
  });

  it('localizes the shared business root menu in both languages', () => {
    expect(zhCN['menu.legacyBusiness']).toBe('业务管理');
    expect(enUS['menu.legacyBusiness']).toBe('Business');
  });
});
