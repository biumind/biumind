// Unit tests for GenerationFormController — 不依赖 Flutter widget / 真 HTTP.

import 'package:biumind/features/creation/application/generation_form_controller.dart';
import 'package:biumind/features/creation/domain/ai_model.dart';
import 'package:flutter_test/flutter_test.dart';

AiModel _videoModel() {
  return AiModel(
    code: 'wanx-2.6-t2v',
    type: 'video',
    displayName: '通义万相 2.6 (视频)',
    providerCode: 'dashscope',
    priceCredits: 40,
    config: const {
      'aspect_ratios': [
        {'key': '16:9', 'value': '1280*720', 'label': '16:9'},
        {'key': '9:16', 'value': '720*1280', 'label': '9:16'},
      ],
      'resolutions': [
        {'key': '720p', 'value': '720p', 'label': '720P'},
        {'key': '1080p', 'value': '1080p', 'label': '1080P'},
      ],
      'duration': {'min': 5, 'max': 15, 'step': 5, 'default': 5},
      'features': {
        'first_frame': 'Y',
        'last_frame': 'Y',
        'reference_image': 'Y',
        'reference_image_count': 1,
      },
    },
  );
}

AiModel _imageModel() {
  return AiModel(
    code: 'wanx-2.6-t2i',
    type: 'image',
    displayName: '通义万相 2.6 (图)',
    providerCode: 'dashscope',
    priceCredits: 20,
    config: const {
      'aspect_ratios': [
        {'key': '1:1', 'value': '1024*1024', 'label': '1:1'},
        {'key': '16:9', 'value': '1280*720', 'label': '16:9'},
      ],
      'resolutions': [
        {'key': '720p', 'value': '720p', 'label': '720P'},
      ],
    },
  );
}

void main() {
  group('GenerationType', () {
    test('wire / fromWire roundtrip', () {
      for (final t in GenerationType.values) {
        expect(GenerationType.fromWire(t.wire), t);
      }
      expect(GenerationType.fromWire('digital_human'),
          GenerationType.digitalHuman);
      expect(GenerationType.fromWire(null), GenerationType.image);
      expect(GenerationType.fromWire('bogus'), GenerationType.image);
    });
  });

  group('canSubmit', () {
    test('需 model + 非空 prompt', () {
      final c = GenerationFormController();
      expect(c.state.canSubmit, isFalse);

      c.setPrompt('柯基');
      expect(c.state.canSubmit, isFalse, reason: '没选 model');

      c.selectModel(_imageModel());
      expect(c.state.canSubmit, isTrue);

      c.setPrompt('   ');
      expect(c.state.canSubmit, isFalse, reason: '纯空白 prompt 视为空');
    });

    test('hotparse 不需要 prompt, 但需要源视频链接', () {
      final c = GenerationFormController()..selectType(GenerationType.hotparse);
      c.selectModel(_imageModel());
      // 无源链接 → 不可提交 (即便有模型, prompt 豁免)
      expect(c.state.canSubmit, isFalse, reason: 'hotparse 缺 source_url');
      c.setHotparseSourceUrl('https://x/v.mp4');
      expect(c.state.canSubmit, isTrue);
    });

    test('hotparse buildParams 发 source_url + source', () {
      final c = GenerationFormController()..selectType(GenerationType.hotparse);
      c.selectModel(_imageModel());
      c.setHotparseSourceUrl('https://x/v.mp4');
      final p = c.state.buildParams();
      expect(p['source_url'], 'https://x/v.mp4');
      expect(p['source'], 'upload');
    });

    test('detectHotparseSource 按 URL 推断平台', () {
      expect(detectHotparseSource('https://www.bilibili.com/video/BV1xx'),
          'bilibili');
      expect(detectHotparseSource('https://b23.tv/abc'), 'bilibili');
      expect(detectHotparseSource('https://v.douyin.com/abc/'), 'douyin');
      expect(detectHotparseSource('https://www.xiaohongshu.com/x'),
          'xiaohongshu');
      expect(detectHotparseSource('https://cdn.example.com/v.mp4'), 'upload');
    });
  });

  group('selectModel 自动填默认参数', () {
    test('视频模型: aspect_ratio + resolution + duration 默认填好', () {
      final c = GenerationFormController()
        ..selectType(GenerationType.video)
        ..selectModel(_videoModel());
      expect(c.state.aspectRatio, '16:9');
      expect(c.state.resolution, '720p');
      expect(c.state.durationSeconds, 5);
    });

    test('用户已显式设的字段不被覆盖', () {
      final c = GenerationFormController()
        ..selectType(GenerationType.video)
        ..setAspectRatio('9:16')
        ..selectModel(_videoModel());
      expect(c.state.aspectRatio, '9:16'); // 用户的选择保留
      expect(c.state.resolution, '720p'); // 这个字段没设, 自动填
    });
  });

  group('selectType 切换', () {
    test('切 type 会清掉 modelCode (避免 type/model mismatch)', () {
      final c = GenerationFormController()..selectModel(_imageModel());
      expect(c.state.modelCode, 'wanx-2.6-t2i');

      c.selectType(GenerationType.video);
      expect(c.state.modelCode, isNull);
      expect(c.state.type, GenerationType.video);
    });

    test('切 type 保留 prompt + isPublic + aiOptimize', () {
      final c = GenerationFormController()
        ..setPrompt('柯基奔跑')
        ..toggleIsPublic()
        ..toggleAiOptimize();

      c.selectType(GenerationType.video);
      expect(c.state.prompt, '柯基奔跑');
      expect(c.state.isPublic, isTrue);
      expect(c.state.aiOptimize, isTrue);
    });
  });

  group('referenceImageUrls', () {
    test('add / remove + 去重 + max 限制', () {
      final c = GenerationFormController();
      c.addReferenceImage('cas:1');
      c.addReferenceImage('cas:2');
      c.addReferenceImage('cas:1'); // 重复
      expect(c.state.referenceImageUrls, ['cas:1', 'cas:2']);

      // max=2 触发上限
      for (var i = 3; i < 10; i++) {
        c.addReferenceImage('cas:$i', max: 2);
      }
      expect(c.state.referenceImageUrls.length, 2);

      c.removeReferenceImage('cas:1');
      expect(c.state.referenceImageUrls, ['cas:2']);
    });
  });

  group('buildParams', () {
    test('视频参数完整投影', () {
      final c = GenerationFormController()
        ..selectType(GenerationType.video)
        ..selectModel(_videoModel())
        ..setFirstFrame('cas:first')
        ..setLastFrame('cas:last')
        ..addReferenceImage('cas:r1')
        ..toggleAiOptimize();

      final p = c.state.buildParams();
      expect(p['aspect_ratio'], '16:9');
      expect(p['resolution'], '720p');
      expect(p['duration_seconds'], 5);
      expect(p['first_frame_url'], 'cas:first');
      expect(p['last_frame_url'], 'cas:last');
      expect(p['reference_image_urls'], ['cas:r1']);
      expect(p['prompt_optimize'], true);
    });

    test('seed=-1 不写入 (服务端按随机处理)', () {
      final c = GenerationFormController()..selectModel(_imageModel());
      expect(c.state.buildParams().containsKey('seed'), isFalse);
      c.setSeed(42);
      expect(c.state.buildParams()['seed'], 42);
    });

    test('numOutputs=1 不写入 (减小 payload)', () {
      final c = GenerationFormController()..selectModel(_imageModel());
      expect(c.state.buildParams().containsKey('num_outputs'), isFalse);
      c.setNumOutputs(3);
      expect(c.state.buildParams()['num_outputs'], 3);
    });
  });

  group('setNumOutputs 边界', () {
    test('clamp 到 [1, 8]', () {
      final c = GenerationFormController();
      c.setNumOutputs(0);
      expect(c.state.numOutputs, 1);
      c.setNumOutputs(99);
      expect(c.state.numOutputs, 8);
      c.setNumOutputs(4);
      expect(c.state.numOutputs, 4);
    });
  });

  group('reset', () {
    test('resetAfterSubmit 保留 model + 参数, 清 prompt + 临时输入', () {
      final c = GenerationFormController()
        ..selectModel(_videoModel())
        ..setPrompt('柯基')
        ..setFirstFrame('cas:f')
        ..addReferenceImage('cas:r1');

      c.resetAfterSubmit();
      expect(c.state.modelCode, 'wanx-2.6-t2v');
      expect(c.state.aspectRatio, '16:9');
      expect(c.state.prompt, '');
      expect(c.state.firstFrameUrl, isNull);
      expect(c.state.referenceImageUrls, isEmpty);
    });

    test('reset 全量回 default', () {
      final c = GenerationFormController()
        ..selectModel(_videoModel())
        ..setPrompt('x');
      c.reset();
      expect(c.state, equals(const GenerationFormState()));
    });
  });

  group('syncFromTask (做同款 / 重新编辑)', () {
    test('完整字段映射', () {
      final c = GenerationFormController();
      c.syncFromTask(
        type: GenerationType.video,
        modelCode: 'wanx-2.6-t2v',
        prompt: '柯基奔跑',
        params: const {
          'aspect_ratio': '9:16',
          'resolution': '1080p',
          'duration_seconds': 10,
          'first_frame_url': 'cas:fa',
          'reference_image_urls': ['cas:r1', 'cas:r2'],
          'prompt_optimize': true,
        },
        isPublic: true,
      );
      expect(c.state.type, GenerationType.video);
      expect(c.state.modelCode, 'wanx-2.6-t2v');
      expect(c.state.aspectRatio, '9:16');
      expect(c.state.resolution, '1080p');
      expect(c.state.durationSeconds, 10);
      expect(c.state.firstFrameUrl, 'cas:fa');
      expect(c.state.referenceImageUrls, ['cas:r1', 'cas:r2']);
      expect(c.state.aiOptimize, isTrue);
      expect(c.state.isPublic, isTrue);
    });
  });
}
