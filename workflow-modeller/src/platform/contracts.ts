export interface JsonFile {
  name: string;
  text: string;
}

export interface Platform {
  /** null means the picker was cancelled; I/O failures reject. */
  openJson(): Promise<JsonFile | null>;
  /** A browser download cannot confirm that the file reached disk. */
  saveJson(name: string, text: string): Promise<'saved' | 'download-started' | 'cancelled'>;
  copyText(text: string): Promise<void>;
  fetch: typeof globalThis.fetch;
}
