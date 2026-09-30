class RO { observe() {} unobserve() {} disconnect() {} }
(globalThis as any).ResizeObserver = (globalThis as any).ResizeObserver ?? RO;
(globalThis as any).DOMMatrixReadOnly = (globalThis as any).DOMMatrixReadOnly ?? class {};
