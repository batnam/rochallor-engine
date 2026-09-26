import { type ReactNode, useCallback, useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

interface PromptOptions {
  initialValue?: string;
  validate?: (value: string) => string | undefined;
}

interface DialogRequest extends PromptOptions {
  kind: 'prompt' | 'confirm';
  message: string;
}

/** Shared modal UI: no browser prompt/confirm or native platform dependency. */
export function useDialogs() {
  const [request, setRequest] = useState<DialogRequest | null>(null);
  const pending = useRef<((value: string | null) => void) | null>(null);

  const finish = useCallback((value: string | null) => {
    pending.current?.(value);
    pending.current = null;
    setRequest(null);
  }, []);

  useEffect(
    () => () => {
      pending.current?.(null);
      pending.current = null;
    },
    [],
  );

  const show = useCallback((next: DialogRequest): Promise<string | null> => {
    if (pending.current) return Promise.resolve(null);
    return new Promise((resolve) => {
      pending.current = resolve;
      setRequest(next);
    });
  }, []);

  const prompt = useCallback(
    (message: string, options: PromptOptions = {}) => show({ kind: 'prompt', message, ...options }),
    [show],
  );
  const confirm = useCallback(
    async (message: string) => (await show({ kind: 'confirm', message })) !== null,
    [show],
  );

  return {
    prompt,
    confirm,
    dialog: request
      ? createPortal(<AppDialog request={request} onFinish={finish} />, document.body)
      : null,
  };
}

function AppDialog({
  request,
  onFinish,
}: {
  request: DialogRequest;
  onFinish: (value: string | null) => void;
}): ReactNode {
  const ref = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const titleId = useId();
  const errorId = useId();
  const [value, setValue] = useState(request.initialValue ?? '');
  const [error, setError] = useState<string>();

  useEffect(() => {
    ref.current?.showModal();
    input.current?.select();
    return () => ref.current?.close();
  }, []);

  return (
    <dialog
      ref={ref}
      className="wm-dialog wm-prompt-dialog"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onFinish(null);
      }}
    >
      <form
        className="wm-dialog-form"
        onSubmit={(event) => {
          event.preventDefault();
          const error = request.validate?.(value);
          if (error) {
            setError(error);
            return;
          }
          onFinish(value);
        }}
      >
        <h2 id={titleId}>{request.message}</h2>
        {request.kind === 'prompt' && (
          <input
            ref={input}
            className="wm-input"
            aria-labelledby={titleId}
            aria-describedby={error ? errorId : undefined}
            aria-invalid={!!error}
            value={value}
            onChange={(event) => {
              setValue(event.target.value);
              setError(undefined);
            }}
          />
        )}
        {error && (
          <p id={errorId} role="alert" className="wm-dialog-errors">
            {error}
          </p>
        )}
        <div className="wm-dialog-actions">
          <button type="button" onClick={() => onFinish(null)}>
            Cancel
          </button>
          <button
            type="submit"
            className="wm-dialog-primary"
            disabled={request.kind === 'prompt' && value.trim() === ''}
          >
            OK
          </button>
        </div>
      </form>
    </dialog>
  );
}
