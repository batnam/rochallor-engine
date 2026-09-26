import { basename } from '@tauri-apps/api/path';
import { writeText } from '@tauri-apps/plugin-clipboard-manager';
import { open, save } from '@tauri-apps/plugin-dialog';
import { readTextFile, writeTextFile } from '@tauri-apps/plugin-fs';
import { fetch } from '@tauri-apps/plugin-http';
import type { Platform } from './contracts';

const filters = [{ name: 'Workflow JSON', extensions: ['json'] }];

export const platform: Platform = {
  async openJson() {
    const path = await open({ multiple: false, directory: false, filters });
    if (path === null) return null;
    return { name: await basename(path), text: await readTextFile(path) };
  },
  async saveJson(name, text) {
    const path = await save({ defaultPath: name, filters });
    if (path === null) return 'cancelled';
    await writeTextFile(path, text);
    return 'saved';
  },
  copyText: writeText,
  fetch,
};
