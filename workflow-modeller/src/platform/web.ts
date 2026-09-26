import type { Platform } from './contracts';

export const platform: Platform = {
  openJson() {
    return new Promise((resolve, reject) => {
      const input = document.createElement('input');
      input.type = 'file';
      input.accept = 'application/json,.json';
      input.hidden = true;
      document.body.append(input);
      input.addEventListener(
        'cancel',
        () => {
          input.remove();
          resolve(null);
        },
        { once: true },
      );
      input.addEventListener(
        'change',
        async () => {
          const file = input.files?.[0];
          input.remove();
          try {
            resolve(file ? { name: file.name, text: await file.text() } : null);
          } catch (error) {
            reject(error);
          }
        },
        { once: true },
      );
      input.click();
    });
  },
  async saveJson(name, text) {
    const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = name;
    document.body.append(link);
    try {
      link.click();
      return 'download-started';
    } finally {
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
  },
  copyText: (text) => navigator.clipboard.writeText(text),
  // Resolve fetch at call time so browser mocks and tests use the same seam.
  fetch: (input, init) => globalThis.fetch(input, init),
};
