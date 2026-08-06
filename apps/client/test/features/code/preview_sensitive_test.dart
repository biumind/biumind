// PreviewGenerator.isSensitivePath — pure function 单测, 钉住敏感文件名
// 匹配规则。规则放宽 (保守拒) 比放紧好, 但误伤要可预测。

import 'package:biumind/features/code/workspace/preview_generator.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('PreviewGenerator.isSensitivePath', () {
    test('dotenv variants', () {
      expect(PreviewGenerator.isSensitivePath('.env'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.env.local'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.env.production'), isTrue);
      expect(PreviewGenerator.isSensitivePath('app/.env'), isTrue);
      expect(PreviewGenerator.isSensitivePath('config/.env.staging'), isTrue);
    });

    test('private key files', () {
      expect(PreviewGenerator.isSensitivePath('.ssh/id_rsa'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.ssh/id_ed25519'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.ssh/id_ed25519.pub'), isTrue); // .pub 仍命中 id_ed25519* 前缀; 公钥误伤可接受 (用户允许列表后续放行)
      expect(PreviewGenerator.isSensitivePath('certs/server.pem'), isTrue);
      expect(PreviewGenerator.isSensitivePath('app/private.key'), isTrue);
      expect(PreviewGenerator.isSensitivePath('keystore.jks'), isTrue);
      expect(PreviewGenerator.isSensitivePath('truststore.p12'), isTrue);
    });

    test('cloud credential paths', () {
      expect(PreviewGenerator.isSensitivePath('.aws/credentials'), isTrue);
      expect(PreviewGenerator.isSensitivePath('home/.aws/config'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.kube/config'), isTrue);
      expect(PreviewGenerator.isSensitivePath('kubeconfig'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.netrc'), isTrue);
      expect(PreviewGenerator.isSensitivePath('.dockercfg'), isTrue);
    });

    test('token / api_key keyword in basename', () {
      expect(PreviewGenerator.isSensitivePath('config/auth_token.txt'), isTrue);
      expect(PreviewGenerator.isSensitivePath('apikey.json'), isTrue);
      expect(PreviewGenerator.isSensitivePath('release.secret'), isTrue);
    });

    test('case-insensitive matching', () {
      expect(PreviewGenerator.isSensitivePath('.ENV'), isTrue);
      expect(PreviewGenerator.isSensitivePath('Server.PEM'), isTrue);
      expect(PreviewGenerator.isSensitivePath('App/ID_RSA'), isTrue);
    });

    test('regular project files do NOT match', () {
      expect(PreviewGenerator.isSensitivePath('lib/main.dart'), isFalse);
      expect(PreviewGenerator.isSensitivePath('README.md'), isFalse);
      expect(PreviewGenerator.isSensitivePath('test/foo_test.dart'), isFalse);
      expect(PreviewGenerator.isSensitivePath('src/auth.ts'), isFalse);
      expect(PreviewGenerator.isSensitivePath('docs/api.md'), isFalse);
      expect(PreviewGenerator.isSensitivePath('package.json'), isFalse);
      expect(PreviewGenerator.isSensitivePath('Dockerfile'), isFalse);
    });

    test('false-positive boundaries that are acceptable', () {
      // "token" 子串命中 — config_token / refresh-token 文件确实可能含 secret,
      // 这种保守拒可接受 (用户可在 settings allowlist 放行)。
      expect(PreviewGenerator.isSensitivePath('refresh_token.json'), isTrue);
      // 但单纯路径里有 token (如 'tokenizer/foo.py') 不命中 — 我们只看 basename
      expect(
        PreviewGenerator.isSensitivePath('tokenizer/vocab.txt'),
        isFalse,
      );
    });
  });
}
