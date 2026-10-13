import { destinationFromPush, dispatchLink, onLink, parseAnyLink, routeFor, takePendingLink } from "@/lib/links";

const T = "11111111-1111-4111-8111-111111111111";

describe("parseAnyLink", () => {
  it("parses team deep links", () => {
    expect(parseAnyLink(`hosbah://t/${T}/apps/app-1`)).toEqual({ team: T, screen: "app", id: "app-1" });
    expect(parseAnyLink(`hosbah://t/${T}/servers/s1`)).toEqual({ team: T, screen: "server", id: "s1" });
    expect(parseAnyLink(`hosbah://t/${T}/domains`)?.screen).toBe("domains");
    expect(parseAnyLink(`hosbah://t/${T}/backups`)?.screen).toBe("backups");
    expect(parseAnyLink(`hosbah://t/${T}/alerts`)?.screen).toBe("alerts");
  });
  it("accepts the bare test-notification link without a team", () => {
    expect(parseAnyLink("hosbah://alerts")).toEqual({ screen: "alerts" });
  });
  it("rejects junk", () => {
    expect(parseAnyLink(null)).toBeNull();
    expect(parseAnyLink("")).toBeNull();
    expect(parseAnyLink("https://evil.example/whatever")).toBeNull();
  });
});

describe("destinationFromPush", () => {
  it("prefers the url, falls back to team + subject", () => {
    expect(destinationFromPush({ team: T, url: `hosbah://t/${T}/apps/a1` })).toEqual({ team: T, screen: "app", id: "a1" });
    expect(destinationFromPush({ team: T, subject_type: "server", subject_id: "s9" })).toMatchObject({ screen: "server", id: "s9" });
    expect(destinationFromPush({ url: "hosbah://alerts" })).toEqual({ screen: "alerts" });
    expect(destinationFromPush(null)).toBeNull();
  });
});

describe("routeFor", () => {
  it("maps destinations to routes", () => {
    expect(routeFor({ screen: "app", id: "a1" })).toEqual({ pathname: "/apps/[id]", params: { id: "a1" } });
    expect(routeFor({ screen: "server", id: "s1" })).toEqual({ pathname: "/servers/[id]", params: { id: "s1" } });
    expect(routeFor({ screen: "domains" })).toEqual({ pathname: "/(tabs)/infra", params: { tab: "domains" } });
    expect(routeFor({ screen: "backups" })).toEqual({ pathname: "/(tabs)/infra", params: { tab: "backups" } });
    expect(routeFor({ screen: "alerts" })).toEqual({ pathname: "/(tabs)/alerts" });
    expect(routeFor({ screen: "app" })).toEqual({ pathname: "/(tabs)/apps" });
  });
});

describe("pending queue", () => {
  it("holds a link until consumed once, and notifies listeners", () => {
    const fn = jest.fn();
    const off = onLink(fn);
    dispatchLink({ screen: "alerts" });
    expect(fn).toHaveBeenCalledTimes(1);
    expect(takePendingLink()).toEqual({ screen: "alerts" });
    expect(takePendingLink()).toBeNull();
    off();
    dispatchLink({ screen: "domains" });
    expect(fn).toHaveBeenCalledTimes(1);
    takePendingLink();
  });
});
