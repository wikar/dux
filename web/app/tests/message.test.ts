import { describe, expect, test } from "bun:test";
import { displayMessage } from "../src/dash/message";

describe("displayMessage", () => {
  test("capitalizes the first word without damaging leading punctuation", () => {
    expect(displayMessage(new Error("semantic error: bad measure"))).toBe("Semantic error: bad measure");
    expect(displayMessage('\"theme\" is reserved')).toBe('\"Theme\" is reserved');
    expect(displayMessage("DUX failed")).toBe("DUX failed");
  });
});
