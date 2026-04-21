{ pkgs, mgmt }:
let
  reloadServiceName = "deploy-notify-reload-proof";
  reloadConfigPath = "/run/${reloadServiceName}.conf";
  reloadRuntimeValuePath = "/run/${reloadServiceName}/value";
  reloadCountPath = "/run/${reloadServiceName}/reload-count";

  restartServiceName = "deploy-notify-restart-proof";
  restartConfigPath = "/run/${restartServiceName}.conf";
  restartRuntimeValuePath = "/run/${restartServiceName}/value";

  reloadStartScript = pkgs.writeShellScript "${reloadServiceName}-start" ''
    value="$(cat ${reloadConfigPath})"
    printf '%s' "$value" > ${reloadRuntimeValuePath}
    exec /run/current-system/sw/bin/sleep infinity
  '';

  reloadReloadScript = pkgs.writeShellScript "${reloadServiceName}-reload" ''
    current=0
    if [ -f ${reloadCountPath} ]; then
      current="$(cat ${reloadCountPath})"
    fi
    next=$((current + 1))
    value="$(cat ${reloadConfigPath})"
    printf '%s' "$value" > ${reloadRuntimeValuePath}
    printf '%s' "$next" > ${reloadCountPath}
  '';

  restartStartScript = pkgs.writeShellScript "${restartServiceName}-start" ''
    value="$(cat ${restartConfigPath})"
    printf '%s' "$value" > ${restartRuntimeValuePath}
    exec /run/current-system/sw/bin/sleep infinity
  '';

  mkMain = pkgs.writeText "main.mcl" ''
    import "deploy"

    file "${reloadConfigPath}" {
      state => $const.res.file.state.exists,
      content => deploy.readfile("/files/${reloadServiceName}.conf"),
      Notify => Svc["${reloadServiceName}"],
    }

    file "${restartConfigPath}" {
      state => $const.res.file.state.exists,
      content => deploy.readfile("/files/${restartServiceName}.conf"),
      Notify => Svc["${restartServiceName}"],
    }

    svc "${reloadServiceName}" {
      state => "running",
      refresh_action => "reload-or-try-restart",
    }

    svc "${restartServiceName}" {
      state => "running",
      refresh_action => "try-restart",
    }

    File["${reloadConfigPath}"] -> Svc["${reloadServiceName}"]
    File["${restartConfigPath}"] -> Svc["${restartServiceName}"]
  '';

  mkMetadata = pkgs.writeText "metadata.yaml" ''
    main: main.mcl
  '';

  mkDeploy = name: value:
    pkgs.runCommand name {} ''
      mkdir -p "$out/files"
      cp ${mkMain} "$out/main.mcl"
      cp ${mkMetadata} "$out/metadata.yaml"
      printf '%s' '${value}' > "$out/files/${reloadServiceName}.conf"
      printf '%s' '${value}' > "$out/files/${restartServiceName}.conf"
    '';

  deployAlpha = mkDeploy "mgmt-deploy-svc-notify-alpha" "alpha";
  deployBravo = mkDeploy "mgmt-deploy-svc-notify-bravo" "bravo";
in
pkgs.testers.runNixOSTest {
  name = "mgmt-deploy-svc-notify-change";

  nodes = {
    target = { pkgs, ... }: {
      environment.systemPackages = [
        mgmt
        pkgs.curl
      ];

      networking.firewall.allowedTCPPorts = [ 2379 ];

      systemd.services.${reloadServiceName} = {
        description = "Deploy notify reload proof service";
        after = [ "network.target" ];
        serviceConfig = {
          Type = "simple";
          RuntimeDirectory = reloadServiceName;
          ExecStart = reloadStartScript;
          ExecReload = reloadReloadScript;
          Restart = "no";
        };
      };

      systemd.services.${restartServiceName} = {
        description = "Deploy notify restart proof service";
        after = [ "network.target" ];
        serviceConfig = {
          Type = "simple";
          RuntimeDirectory = restartServiceName;
          ExecStart = restartStartScript;
          Restart = "no";
        };
      };

      system.stateVersion = "24.11";
    };

    deployer = { pkgs, ... }: {
      environment.systemPackages = [
        mgmt
        pkgs.curl
      ];

      system.stateVersion = "24.11";
    };
  };

  testScript = ''
    start_all()

    target.wait_for_unit("multi-user.target")
    deployer.wait_for_unit("multi-user.target")

    with subtest("start target mgmt instance"):
        target.succeed("set -euo pipefail; ${mgmt}/bin/mgmt run --hostname deploy-notify-target --tmp-prefix --no-pgp --client-urls=http://192.168.1.2:2379 --server-urls=http://192.168.1.2:2380 --converged-timeout=-1 empty >/tmp/mgmt-run.log 2>&1 & echo $! >/tmp/mgmt-run.pid; sleep 2; kill -0 $(cat /tmp/mgmt-run.pid)")
        target.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'target mgmt health never became ready' >&2; cat /tmp/mgmt-run.log >&2; exit 1")
        deployer.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'deployer could not reach target mgmt health endpoint' >&2; exit 1")

    with subtest("deploy initial config-managed services and capture baseline"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployAlpha}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${reloadServiceName}.service && test \"$(cat ${reloadRuntimeValuePath} 2>/dev/null || true)\" = alpha && systemctl is-active --quiet ${restartServiceName}.service && test \"$(cat ${restartRuntimeValuePath} 2>/dev/null || true)\" = alpha && exit 0; sleep 1; done; echo 'initial notify deploy never converged' >&2; systemctl status ${reloadServiceName}.service >&2 || true; systemctl status ${restartServiceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed("test ! -e ${reloadCountPath}")
        reload_initial_pid = target.succeed("systemctl show -P MainPID ${reloadServiceName}.service").strip()
        reload_initial_start = target.succeed("systemctl show -P ExecMainStartTimestampMonotonic ${reloadServiceName}.service").strip()
        restart_initial_pid = target.succeed("systemctl show -P MainPID ${restartServiceName}.service").strip()
        restart_initial_start = target.succeed("systemctl show -P ExecMainStartTimestampMonotonic ${restartServiceName}.service").strip()
        assert reload_initial_pid != "0", f"unexpected reload service MainPID: {reload_initial_pid}"
        assert restart_initial_pid != "0", f"unexpected restart service MainPID: {restart_initial_pid}"

    with subtest("deploy updated config and prove notify-driven reload and restart"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployBravo}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${reloadServiceName}.service && test \"$(cat ${reloadRuntimeValuePath} 2>/dev/null || true)\" = bravo && test \"$(cat ${reloadCountPath} 2>/dev/null || true)\" = 1 && systemctl is-active --quiet ${restartServiceName}.service && test \"$(cat ${restartRuntimeValuePath} 2>/dev/null || true)\" = bravo && exit 0; sleep 1; done; echo 'updated notify deploy never converged' >&2; systemctl status ${reloadServiceName}.service >&2 || true; systemctl status ${restartServiceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed(f"pid=\"$(systemctl show -P MainPID ${reloadServiceName}.service)\"; test \"$pid\" = \"{reload_initial_pid}\"")
        target.succeed(f"start=\"$(systemctl show -P ExecMainStartTimestampMonotonic ${reloadServiceName}.service)\"; test \"$start\" = \"{reload_initial_start}\"")
        target.succeed(f"for i in $(seq 1 30); do pid=\"$(systemctl show -P MainPID ${restartServiceName}.service)\"; start=\"$(systemctl show -P ExecMainStartTimestampMonotonic ${restartServiceName}.service)\"; if [ \"$pid\" != \"{restart_initial_pid}\" ] && [ \"$start\" != \"{restart_initial_start}\" ]; then exit 0; fi; sleep 1; done; echo 'restart service never restarted after config change' >&2; systemctl status ${restartServiceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed("! grep -Fq 'exec[systemctl daemon-reload]' /tmp/mgmt-run.log")

    target.succeed("kill $(cat /tmp/mgmt-run.pid)")
  '';
}
