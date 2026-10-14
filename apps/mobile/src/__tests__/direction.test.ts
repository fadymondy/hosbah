import { startAlign, writingDirection } from "@/lib/direction";

describe("startAlign", () => {
  it("pre-swaps for the RTL layout so Arabic text starts on the right", () => {
    // React Native swaps left/right under an RTL layout, so "left" renders on the right.
    expect(startAlign(true, true)).toBe("left");
  });
  it("aligns Arabic right when the RTL layout flag has not applied yet", () => {
    expect(startAlign(true, false)).toBe("right");
  });
  it("keeps English on the left in either layout", () => {
    expect(startAlign(false, false)).toBe("left");
    expect(startAlign(false, true)).toBe("right");
  });
});

describe("writingDirection", () => {
  it("follows the language", () => {
    expect(writingDirection(true)).toBe("rtl");
    expect(writingDirection(false)).toBe("ltr");
  });
});
