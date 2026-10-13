import { useLocalSearchParams } from "expo-router";
import { useEffect, useState } from "react";

import { usePrefs } from "@/lib/prefs";
import { useCertificates, useCurrentTeam, useDataServices, useDomains } from "@/lib/queries";
import { BackupRow, CertRow, joinDomains } from "@/ui/infra-bits";
import { Screen } from "@/ui/screen";
import { Segmented } from "@/ui/segmented";
import { QueryBoundary } from "@/ui/states";
import { TeamPicker } from "@/ui/team-picker";

type Tab = "domains" | "backups";

export default function Infra() {
  const { t } = usePrefs();
  const params = useLocalSearchParams<{ tab?: string }>();
  const [tab, setTab] = useState<Tab>("domains");
  // Deep links (hosbah://t/<team>/domains | backups) and pushes choose the segment.
  useEffect(() => {
    if (params.tab === "domains" || params.tab === "backups") setTab(params.tab);
  }, [params.tab]);

  const { loading, error } = useCurrentTeam();
  const domains = useDomains();
  const certs = useCertificates();
  const services = useDataServices();
  const rows = joinDomains(domains.data ?? [], certs.data ?? []);
  const refresh = () => {
    void domains.refetch();
    void certs.refetch();
    void services.refetch();
  };

  return (
    <Screen
      title={t(tab === "domains" ? "domain.title" : "backup.title")}
      right={<TeamPicker />}
      refreshing={domains.isRefetching || services.isRefetching}
      onRefresh={refresh}
      header={
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: "domains", label: t("tab.domains"), testID: "segment-domains" },
            { value: "backups", label: t("tab.backups"), testID: "segment-backups" },
          ]}
        />
      }
      testID="screen-infra"
    >
      {tab === "domains" ? (
        <QueryBoundary
          loading={loading || domains.isLoading}
          error={error ?? domains.error}
          hasData={!!domains.data}
          empty={rows.length === 0}
          emptyText={t("domain.empty")}
          onRetry={refresh}
        >
          {rows.map((r) => (
            <CertRow key={r.host} domain={r.host} cert={r.cert} appName={r.appName} />
          ))}
        </QueryBoundary>
      ) : (
        <QueryBoundary
          loading={loading || services.isLoading}
          error={error ?? services.error}
          hasData={!!services.data}
          empty={services.data?.length === 0}
          emptyText={t("backup.empty")}
          onRetry={refresh}
        >
          {(services.data ?? []).map((s) => (
            <BackupRow key={s.id} service={s} />
          ))}
        </QueryBoundary>
      )}
    </Screen>
  );
}
