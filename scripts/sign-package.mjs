import { createHash, createPrivateKey, sign } from "node:crypto";
import { createReadStream } from "node:fs";
import { open, readFile } from "node:fs/promises";

const [fileName, keyPath] = process.argv.slice(2);
const key = createPrivateKey(await readFile(keyPath));
if (key.asymmetricKeyType !== "ed25519") throw new Error("Use an Ed25519 signing key");
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
