import { platform } from '@/platform/desktop';
import { basename } from '@tauri-apps/api/path';
import { open, save } from '@tauri-apps/plugin-dialog';
import { readTextFile, writeTextFile } from '@tauri-apps/plugin-fs';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@tauri-apps/api/path', () => ({ basename: vi.fn() }));
vi.mock('@tauri-apps/plugin-dialog', () => ({ open: vi.fn(), save: vi.fn() }));
vi.mock('@tauri-apps/plugin-fs', () => ({ readTextFile: vi.fn(), writeTextFile: vi.fn() }));

beforeEach(() => vi.resetAllMocks());

describe('desktop JSON files', () => {
  it('returns the selected file content and name, including non-ASCII paths', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/Quy trình.json');
    vi.mocked(basename).mockResolvedValue('Quy trình.json');
    vi.mocked(readTextFile).mockResolvedValue('{"name":"Quy trình"}');
    await expect(platform.openJson()).resolves.toEqual({
      name: 'Quy trình.json',
      text: '{"name":"Quy trình"}',
    });
    expect(readTextFile).toHaveBeenCalledWith('/tmp/Quy trình.json');
  });

  it('does no I/O when either picker is cancelled', async () => {
    vi.mocked(open).mockResolvedValue(null);
    vi.mocked(save).mockResolvedValue(null);
    await expect(platform.openJson()).resolves.toBeNull();
    await expect(platform.saveJson('workflow.json', '{}')).resolves.toBe('cancelled');
    expect(readTextFile).not.toHaveBeenCalled();
    expect(writeTextFile).not.toHaveBeenCalled();
  });

  it('writes the exported text to the path selected by the user', async () => {
    vi.mocked(save).mockResolvedValue('/tmp/edited.json');
    await expect(platform.saveJson('workflow.json', '{"steps":[]}')).resolves.toBe('saved');
    expect(writeTextFile).toHaveBeenCalledWith('/tmp/edited.json', '{"steps":[]}');
  });

  it('propagates read and write failures instead of reporting success', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/unreadable.json');
    vi.mocked(save).mockResolvedValue('/tmp/unwritable.json');
    vi.mocked(readTextFile).mockRejectedValue(new Error('Read denied'));
    vi.mocked(writeTextFile).mockRejectedValue(new Error('Disk full'));
    await expect(platform.openJson()).rejects.toThrow('Read denied');
    await expect(platform.saveJson('workflow.json', '{}')).rejects.toThrow('Disk full');
  });
});
