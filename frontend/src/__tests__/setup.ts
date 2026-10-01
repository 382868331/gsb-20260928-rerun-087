class RO { observe() {} unobserve() {} disconnect() {} }
(globalThis as any).ResizeObserver = (globalThis as any).ResizeObserver ?? RO;
(globalThis as any).DOMMatrixReadOnly = (globalThis as any).DOMMatrixReadOnly ?? class {};
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => cleanup());
