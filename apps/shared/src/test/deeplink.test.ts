import { describe, expect, it } from "vitest";
import { parseDeepLink, pushDataToLink, toDeepLink } from "../deeplink";

describe("parseDeepLink", () => {
  it("parses the custom scheme", () => {
    expect(parseDeepLink("hosbah://t/acme/apps/42")).toEqual({ team: "acme", screen: "app", id: "42" });
    expect(parseDeepLink("hosbah://t/acme/servers/s1")).toEqual({ team: "acme", screen: "server", id: "s1" });
    expect(parseDeepLink("hosbah://t/acme/domains")).toEqual({ team: "acme", screen: "domains" });
    expect(parseDeepLink("hosbah://t/acme/backups/")).toEqual({ team: "acme", screen: "backups" });
    expect(parseDeepLink("hosbah://t/acme/alerts")).toEqual({ team: "acme", screen: "alerts" });
  });
  it("parses https links on any host, ignoring query", () => {
    expect(parseDeepLink("https://app.hosbah.com/t/acme/apps/7?x=1")).toEqual({ team: "acme", screen: "app", id: "7" });
  });
  it("decodes segments", () => {
    expect(parseDeepLink("hosbah://t/my%20team/apps/a%2Fb")).toEqual({ team: "my team", screen: "app", id: "a/b" });
  });
  it("rejects junk", () => {
    for (const u of [null, "", "hosbah://x/y", "hosbah://t/acme", "hosbah://t/acme/apps", "hosbah://t/acme/nope", "https://x.com/other/path", "hosbah://t/acme/apps/1/extra", "hosbah://t/%E0%A4%A/apps/1"]) {
      expect(parseDeepLink(u)).toBeNull();
    }
  });
  it("round-trips", () => {
    const l = { team: "a b", screen: "app" as const, id: "1" };
    expect(parseDeepLink(toDeepLink(l))).toEqual(l);
  });
});

describe("pushDataToLink", () => {
  it("prefers a valid url", () => {
    expect(pushDataToLink({ url: "hosbah://t/a/apps/1", team: "b" })).toEqual({ team: "a", screen: "app", id: "1" });
  });
  it("derives from the subject", () => {
    expect(pushDataToLink({ team: "a", subject_type: "app", subject_id: "9" })).toEqual({ team: "a", screen: "app", id: "9" });
    expect(pushDataToLink({ team: "a", subject_type: "server", subject_id: "s" })).toEqual({ team: "a", screen: "server", id: "s" });
    expect(pushDataToLink({ team: "a", subject_type: "certificate" })).toEqual({ team: "a", screen: "domains" });
    expect(pushDataToLink({ team: "a", subject_type: "data_service", subject_id: "d" })).toEqual({ team: "a", screen: "backups" });
    expect(pushDataToLink({ team: "a", subject_type: "weird" })).toEqual({ team: "a", screen: "alerts" });
    expect(pushDataToLink({ team: "a", subject_type: "app" })).toEqual({ team: "a", screen: "alerts" });
  });
  it("is null without a team or data", () => {
    expect(pushDataToLink({ kind: "x" })).toBeNull();
    expect(pushDataToLink(null)).toBeNull();
    expect(pushDataToLink({ url: "garbage" })).toBeNull();
  });
});
