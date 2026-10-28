import { describe, expect, it } from "vitest";
import { formatBytes } from "./format";

describe("formatBytes", () => {
  it("it prints whole bytes under a kilobyte", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  // The rate is bytes over elapsed seconds, so it is not an integer. This is
  // the case that leaked onto the live page as "512.5919323304289 B/s".
  it("it rounds a fractional rate instead of printing every digit", () => {
    expect(formatBytes(512.5919323304289)).toBe("513 B");
    expect(formatBytes(0.4)).toBe("0 B");
  });

  it("it switches to KB and MB at the binary boundaries", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1048575)).toBe("1024.0 KB");
    expect(formatBytes(1048576)).toBe("1.00 MB");
    expect(formatBytes(5 * 1048576)).toBe("5.00 MB");
  });
});
