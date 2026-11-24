import {
  createHash,
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  sign,
} from "node:crypto";
import { createReadStream } from "node:fs";
import { link, mkdir, mkdtemp, open, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

const [fileName, keyPath] = process.argv.slice(2);
const ensureKey = fileName === "--ensure-public-key";
const key = createPrivateKey(ensureKey ? await readOrCreateKey(keyPath) : await readFile(keyPath));
if (key.asymmetricKeyType !== "ed25519") throw new Error("Use an Ed25519 signing key");

if (fileName === "--public-key" || ensureKey) {
  process.stdout.write(createPublicKey(key).export({ type: "spki", format: "pem" }));
} else {
  await signPackage(fileName, key);
}

async function readOrCreateKey(keyPath) {
  try {
    return await readFile(keyPath);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }

  const directory = dirname(keyPath);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const staging = await mkdtemp(join(directory, ".update-signing-"));
  try {
    const keyFile = join(staging, "key.pem");
    const { privateKey } = generateKeyPairSync("ed25519");
    await writeFile(keyFile, privateKey.export({ type: "pkcs8", format: "pem" }), { mode: 0o600 });
    try {
      await link(keyFile, keyPath);
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
    }
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
  return readFile(keyPath);
}

async function signPackage(fileName, key) {
  const file = await open(fileName, "r+");
  try {
    const header = Buffer.alloc(512);
    await file.read(header, 0, header.length, 0);
    const marker = Buffer.from("# sailfin-signature: ");
    const position = header.indexOf(marker) + marker.length;
    if (position < marker.length || !header.subarray(position, position + 128).equals(Buffer.alloc(128, "0"))) {
      throw new Error("Missing signature placeholder");
    }
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(fileName)) hash.update(chunk);
    await file.write(sign(null, hash.digest(), key).toString("hex"), position, "ascii");
  } finally {
    await file.close();
  }
}
