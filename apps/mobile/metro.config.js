// Metro for the Hosbah app.
//  - keeps the file watcher out of the native build trees (android/ and ios/ fill with tens of thousands
//    of files after a Gradle build; resolver.blockList is also the file map's ignore pattern);
//  - watches ../shared, the @hosbah/client workspace package (installed as a symlink via file:../shared).
const path = require("node:path");
const { getDefaultConfig } = require("expo/metro-config");

const projectRoot = __dirname;
const shared = path.resolve(projectRoot, "..", "shared");

const config = getDefaultConfig(projectRoot);

const blocked = [
  /[\\/]android[\\/](app[\\/]build|build|\.gradle|\.cxx|app[\\/]\.cxx)[\\/].*/,
  /[\\/]ios[\\/](build|Pods|DerivedData)[\\/].*/,
  /[\\/]shared[\\/]node_modules[\\/].*/,
  /.*\.log$/,
];
const existing = config.resolver.blockList;
config.resolver.blockList = [...(Array.isArray(existing) ? existing : existing ? [existing] : []), ...blocked];

config.watchFolders = [...(config.watchFolders ?? []), shared];
// Everything the shared package imports resolves from the app's node_modules.
config.resolver.nodeModulesPaths = [path.join(projectRoot, "node_modules")];

module.exports = config;
