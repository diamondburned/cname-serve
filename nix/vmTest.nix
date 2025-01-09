{ self, pkgs }:

pkgs.nixosTest {
  name = "moduleConfig";
  nodes.machine =
    { ... }:
    {
      imports = [
        self.nixosModules.default
      ];

      services.cname-serve = {
        enable = true;
        config = {
          # Mimics example config.
          fallback_dns = "1.1.1.1:53";
          zones."d14.place" = {
            ha = "bridget.skate-gopher.ts.net";
          };
        };
        verbose = true;
      };

      environment.systemPackages = with pkgs; [
        dig
      ];
    };
  testScript =
    { ... }:
    ''
      import time

      machine.wait_for_unit("cname-serve.service")

      def try_command(cmd: str):
        for i in range(20):
          print("Attempt", i)

          out: str | Exception
          try:
            out = machine.succeed("nslookup ha.d14.place 127.0.0.1")
            if out.find("canonical name = bridget.skate-gopher.ts.net.") == -1:
              raise Exception(f"Unexpected output: {out}")
            break
          except Exception as e:
            out = e

          time.sleep(0.1)
        else:
          raise Exception(out)

      try_command("nslookup ha.d14.place 127.0.0.1")
      try_command("nslookup google.com   127.0.0.1")
    '';
}
