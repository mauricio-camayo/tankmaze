export type ParseError = { line: number; col: number; message: string };

let ready: Promise<void> | null = null;

function ensureLoaded(): Promise<void> {
  if (ready) return ready;
  ready = (async () => {
    await new Promise<void>((resolve, reject) => {
      if ((window as any).Go) { resolve(); return; }
      const s = document.createElement('script');
      s.src = '/wasm_exec.js';
      s.onload = () => resolve();
      s.onerror = () => reject(new Error('wasm_exec.js failed to load'));
      document.head.appendChild(s);
    });
    const go = new (window as any).Go();
    const result = await WebAssembly.instantiateStreaming(fetch('/syntax-check.wasm'), go.importObject);
    go.run(result.instance);
  })();
  return ready;
}

export async function syntaxCheck(src: string): Promise<ParseError[]> {
  await ensureLoaded();
  const fn = (window as any).goSyntaxCheck;
  if (typeof fn !== 'function') return [];
  try {
    return JSON.parse(fn(src)) as ParseError[];
  } catch {
    return [];
  }
}
