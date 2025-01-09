{
  src,
  lib,
  buildGoModule,
}:

buildGoModule {
  pname = "cname-serve";
  version = src.rev or "unknown";
  inherit src;
  subPackages = [ "." ];
  vendorHash = builtins.readFile ./vendorHash;

  meta = {
    description = "DNS server that only serves CNAME records.";
    homepage = "https://libdb.so/cname-serve";
    license = lib.licenses.mit;
    mainProgram = "cname-serve";
  };
}
