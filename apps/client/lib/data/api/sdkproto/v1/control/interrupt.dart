import 'package:json_annotation/json_annotation.dart';

part 'interrupt.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class Interrupt {
  final String subtype;

  Interrupt({this.subtype = 'interrupt'});

  factory Interrupt.fromJson(Map<String, dynamic> json) =>
      _$InterruptFromJson(json);
  Map<String, dynamic> toJson() => _$InterruptToJson(this);
}
