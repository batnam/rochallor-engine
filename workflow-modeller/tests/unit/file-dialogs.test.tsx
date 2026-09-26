import { ExportDialog } from '@/panels/ExportDialog';
import { ImportDialog } from '@/panels/ImportDialog';
import { useWorkflowStore } from '@/store/workflowStore';
import { platform } from '@platform';
import { cleanup, render } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

beforeEach(() => useWorkflowStore.getState().reset());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it('keeps pasted JSON when the file picker is cancelled or reading fails', async () => {
  const picker = vi.spyOn(platform, 'openJson').mockResolvedValue(null);
  const user = userEvent.setup();
  const view = render(<ImportDialog open onClose={() => {}} />);
  const text = view.getByRole('textbox');
  await user.type(text, 'existing draft');
  await user.click(view.getByRole('button', { name: 'Choose JSON file' }));
  expect((text as HTMLTextAreaElement).value).toBe('existing draft');
  picker.mockRejectedValueOnce(new Error('Read denied'));
  await user.click(view.getByRole('button', { name: 'Choose JSON file' }));
  expect(view.getByText('Read denied')).toBeTruthy();
  expect((text as HTMLTextAreaElement).value).toBe('existing draft');
});

it('reports save and clipboard errors and distinguishes a download from a saved file', async () => {
  const save = vi.spyOn(platform, 'saveJson').mockRejectedValueOnce(new Error('Disk full'));
  vi.spyOn(platform, 'copyText').mockRejectedValue(new Error('Clipboard denied'));
  const user = userEvent.setup();
  const view = render(<ExportDialog open onClose={() => {}} />);
  const button = view.getByRole('button', { name: 'Save JSON' });
  await user.click(button);
  expect(view.getByRole('alert').textContent).toBe('Disk full');
  save.mockResolvedValueOnce('download-started');
  await user.click(button);
  expect(view.getByRole('status').textContent).toBe('Download started.');
  save.mockResolvedValueOnce('cancelled');
  await user.click(button);
  expect(view.queryByRole('status')).toBeNull();
  save.mockResolvedValueOnce('saved');
  await user.click(button);
  expect(view.getByRole('status').textContent).toBe('File saved.');
  await user.click(view.getByRole('button', { name: 'Copy to clipboard' }));
  expect(view.getByRole('alert').textContent).toBe('Clipboard denied');
});
