// 仅由显式方法调用管理 WFP；加载/编译本文件不会打开应用或安装过滤器。
// x64 Windows ABI。规则仅匹配：指定 EXE 的 ALE_APP_ID AND 未设置 IS_LOOPBACK。
// 本机回环不被此规则阻断；这里不把允许范围收窄到某个代理端口。
// persistent=false 是静态运行期规则，仍需 Remove，非随句柄关闭自动删除的 dynamic session。
// 没有 BOOTTIME 规则，没有包身份或更新路径通配匹配，没有全局枚举/删除。
// 定义依据：microsoft/win32metadata 的 shared/fwptypes.h、shared/fwpmtypes.h、um/fwpmu.h。
using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;

namespace ClaudeDesktopNetworkGuard
{
    public sealed class SubLayerInfo
    {
        public Guid Key { get; internal set; }
        public uint Flags { get; internal set; }
        public ushort Weight { get; internal set; }
        public bool ProviderIsNull { get; internal set; }
        public string Name { get; internal set; }
        public bool MatchesExpected { get; internal set; }
    }

    public sealed class FilterInfo
    {
        public Guid Key { get; internal set; }
        public Guid Layer { get; internal set; }
        public Guid SubLayer { get; internal set; }
        public ulong Id { get; internal set; }
        public uint Flags { get; internal set; }
        public uint Action { get; internal set; }
        public uint ConditionCount { get; internal set; }
        public string Name { get; internal set; }
        public string NativeAppPath { get; internal set; }
        public byte[] AppId { get; internal set; }
        public bool ProviderIsNull { get; internal set; }
        public bool ExactAppIdCondition { get; internal set; }
        public bool NonLoopbackCondition { get; internal set; }
        public bool IsOwnedExactBlock { get; internal set; }
        public bool Enabled { get { return (Flags & 0x20u) == 0; } }
        public bool Persistent { get { return (Flags & 1u) != 0; } }
    }

    public static class DirectConnectionFilter
    {
        public static readonly Guid SubLayerKey = new Guid("3f450820-5b6a-4ee2-bbbe-714d1fcaec6c");
        public static readonly Guid LayerV4 = new Guid("c38d57d1-05a7-4c33-904f-7fbceee60e82");
        public static readonly Guid LayerV6 = new Guid("4a72393b-319f-44bc-84c3-ba54dcb3b6b4");
        private static readonly Guid AppIdKey = new Guid("d78e1e87-8644-4ea5-9437-d809ecefc971");
        private static readonly Guid FlagsKey = new Guid("632ce23b-5167-435c-86d7-e903684aa80c");
        private const string SubLayerName = "Claude dedicated direct-connect guard v1";
        private const string FilterName = "Claude exact-app non-loopback block v1";
        // 本机首次创建后只读返回的实际权重为 0x8001；保持该值及严格归属校验。
        // 不据此推断 Windows 的通用权重调整规则。
        private const ushort SubLayerWeight = 0x8001;
        private const uint PersistentFlag = 1, DisabledFlag = 0x20, IndexedFlag = 0x40;
        private const uint ActionBlock = 0x1001, LoopbackFlag = 1;
        private const int MatchEqual = 0, MatchFlagsNoneSet = 8;
        private const int TypeEmpty = 0, TypeUint32 = 3, TypeByteBlob = 12;
        private const uint FilterNotFound = 0x80320003, SubLayerNotFound = 0x80320007;

        // 不调用本机 API。任何 ABI 不符都在打开 WFP 引擎之前失败。
        public static string ValidateAbi()
        {
            if (IntPtr.Size != 8) throw new PlatformNotSupportedException("Requires a 64-bit process.");
            Size<DisplayData>(16); Size<ByteBlob>(16); Size<Value>(16);
            Size<FilterCondition>(40); Size<FilterAction>(20); Size<FilterContext>(16);
            Size<NativeFilter>(200); Size<NativeSubLayer>(72);
            Offset<ByteBlob>("Data", 8); Offset<Value>("Pointer", 8);
            Offset<FilterCondition>("MatchType", 16); Offset<FilterCondition>("ConditionValue", 24);
            Offset<NativeFilter>("Flags", 32); Offset<NativeFilter>("ProviderKey", 40);
            Offset<NativeFilter>("ProviderData", 48); Offset<NativeFilter>("LayerKey", 64);
            Offset<NativeFilter>("SubLayerKey", 80); Offset<NativeFilter>("Weight", 96);
            Offset<NativeFilter>("NumFilterConditions", 112); Offset<NativeFilter>("Conditions", 120);
            Offset<NativeFilter>("Action", 128); Offset<NativeFilter>("Context", 152);
            Offset<NativeFilter>("Reserved", 168); Offset<NativeFilter>("FilterId", 176);
            Offset<NativeFilter>("EffectiveWeight", 184);
            Offset<NativeSubLayer>("Flags", 32); Offset<NativeSubLayer>("ProviderKey", 40);
            Offset<NativeSubLayer>("ProviderData", 48); Offset<NativeSubLayer>("Weight", 64);
            return "Windows x64 ABI OK: FILTER=200 CONDITION=40 SUBLAYER=72 VALUE=16";
        }

        // 两条规则同一事务添加。已有 GUID 必须与路径、层、动作、条件和持久性完全一致。
        public static FilterInfo[] Add(string appPath, Guid ipv4Key, Guid ipv6Key, bool persistent)
        {
            ValidateKeys(new Guid[] { ipv4Key, ipv6Key });
            string path = ValidatePath(appPath, true);
            ValidateAbi();
            IntPtr appId = IntPtr.Zero;
            try
            {
                Check(Native.FwpmGetAppIdFromFileName0(path, out appId), "Get exact application ID");
                byte[] expected = ReadBlob(appId);
                using (Engine engine = new Engine())
                using (Arena arena = new Arena())
                {
                    bool transaction = false;
                    try
                    {
                        Check(Native.FwpmTransactionBegin0(engine.Handle, 0), "Begin add transaction");
                        transaction = true;
                        EnsureSubLayer(engine.Handle, arena);
                        AddOne(engine.Handle, arena, appId, expected, ipv4Key, LayerV4, persistent);
                        AddOne(engine.Handle, arena, appId, expected, ipv6Key, LayerV6, persistent);
                        FilterInfo a = RequireMatch(ReadFilter(engine.Handle, ipv4Key), expected, LayerV4, persistent);
                        FilterInfo b = RequireMatch(ReadFilter(engine.Handle, ipv6Key), expected, LayerV6, persistent);
                        Check(Native.FwpmTransactionCommit0(engine.Handle), "Commit add transaction");
                        transaction = false;
                        return new FilterInfo[] { a, b };
                    }
                    finally
                    {
                        if (transaction) Check(Native.FwpmTransactionAbort0(engine.Handle), "Abort add transaction");
                    }
                }
            }
            finally { if (appId != IntPtr.Zero) Native.FwpmFreeMemory0(ref appId); }
        }

        // 只读按 GUID 获取；不存在返回 null。返回完整范围校验结果，不能仅凭存在判为有效。
        public static FilterInfo Get(Guid key)
        {
            ValidateKeys(new Guid[] { key });
            using (Engine engine = new Engine()) return ReadFilter(engine.Handle, key);
        }

        // 只读诊断固定子层；读取权限与 EngineOpen 权限不同，可能需要管理员。
        public static SubLayerInfo GetSubLayer()
        {
            using (Engine engine = new Engine()) return ReadSubLayer(engine.Handle);
        }

        // 用 Windows 生成的原生路径 blob 比较，避免自行猜测 DOS 路径到设备路径的转换。
        public static FilterInfo[] Verify(string appPath, Guid ipv4Key, Guid ipv6Key, bool persistent)
        {
            ValidateKeys(new Guid[] { ipv4Key, ipv6Key });
            string path = ValidatePath(appPath, true);
            ValidateAbi();
            IntPtr appId = IntPtr.Zero;
            try
            {
                Check(Native.FwpmGetAppIdFromFileName0(path, out appId), "Get verification application ID");
                byte[] expected = ReadBlob(appId);
                using (Engine engine = new Engine())
                    return new FilterInfo[] {
                        RequireMatch(ReadFilter(engine.Handle, ipv4Key), expected, LayerV4, persistent),
                        RequireMatch(ReadFilter(engine.Handle, ipv6Key), expected, LayerV6, persistent)
                    };
            }
            finally { if (appId != IntPtr.Zero) Native.FwpmFreeMemory0(ref appId); }
        }

        public static int Remove(Guid ipv4Key, Guid ipv6Key)
        {
            return Remove(new Guid[] { ipv4Key, ipv6Key });
        }

        // 仅删除 caller 给出的、确属本 helper 的规则；不枚举、不删除子层或其他规则。
        // 空子层故意保留：它自身不产生 permit/block 行为。
        public static int Remove(Guid[] keys)
        {
            ValidateKeys(keys);
            using (Engine engine = new Engine())
            {
                bool transaction = false;
                try
                {
                    Check(Native.FwpmTransactionBegin0(engine.Handle, 0), "Begin remove transaction");
                    transaction = true;
                    int removed = 0;
                    foreach (Guid key in keys)
                    {
                        FilterInfo info = ReadFilter(engine.Handle, key);
                        if (info == null) continue;
                        if (!info.IsOwnedExactBlock)
                            throw new InvalidOperationException("Refusing to remove a foreign or changed filter: " + key);
                        Guid exactKey = key;
                        Check(Native.FwpmFilterDeleteByKey0(engine.Handle, ref exactKey), "Delete exact filter " + key);
                        removed++;
                    }
                    Check(Native.FwpmTransactionCommit0(engine.Handle), "Commit remove transaction");
                    transaction = false;
                    return removed;
                }
                finally
                {
                    if (transaction) Check(Native.FwpmTransactionAbort0(engine.Handle), "Abort remove transaction");
                }
            }
        }

        private static void AddOne(IntPtr engine, Arena arena, IntPtr appId, byte[] expected,
            Guid key, Guid layer, bool persistent)
        {
            FilterInfo existing = ReadFilter(engine, key);
            if (existing != null) { RequireMatch(existing, expected, layer, persistent); return; }
            FilterCondition app = new FilterCondition {
                FieldKey = AppIdKey, MatchType = MatchEqual,
                ConditionValue = new Value { Type = TypeByteBlob, Pointer = appId }
            };
            FilterCondition notLoopback = new FilterCondition {
                FieldKey = FlagsKey, MatchType = MatchFlagsNoneSet,
                ConditionValue = new Value { Type = TypeUint32, Uint32 = LoopbackFlag }
            };
            int stride = Marshal.SizeOf(typeof(FilterCondition));
            IntPtr conditions = arena.Allocate(checked(stride * 2));
            Marshal.StructureToPtr(app, conditions, false);
            Marshal.StructureToPtr(notLoopback, IntPtr.Add(conditions, stride), false);
            NativeFilter filter = new NativeFilter {
                Key = key,
                Display = new DisplayData { Name = arena.String(FilterName), Description = arena.String("Exact EXE AND not IS_LOOPBACK; block outbound IPv4/IPv6 direct connections.") },
                Flags = persistent ? PersistentFlag : 0,
                ProviderKey = IntPtr.Zero,
                LayerKey = layer, SubLayerKey = SubLayerKey,
                Weight = new Value { Type = TypeEmpty },
                NumFilterConditions = 2, Conditions = conditions,
                Action = new FilterAction { Type = ActionBlock }
            };
            ulong id;
            Check(Native.FwpmFilterAdd0(engine, ref filter, IntPtr.Zero, out id), "Add exact filter " + key);
        }

        private static void EnsureSubLayer(IntPtr engine, Arena arena)
        {
            Guid key = SubLayerKey;
            SubLayerInfo existing = ReadSubLayer(engine);
            if (existing != null)
            {
                if (!existing.MatchesExpected)
                    throw new InvalidOperationException("The fixed sublayer differs: Key=" + existing.Key +
                        "; Flags=0x" + existing.Flags.ToString("X8") + " (expected 0x00000001)" +
                        "; Weight=" + existing.Weight + " (expected " + SubLayerWeight + ")" +
                        "; ProviderIsNull=" + existing.ProviderIsNull + " (expected True)" +
                        "; Name=[" + existing.Name + "] (expected [" + SubLayerName + "]).");
                return;
            }
            NativeSubLayer value = new NativeSubLayer {
                Key = key,
                Display = new DisplayData { Name = arena.String(SubLayerName), Description = arena.String("Only explicitly supplied application paths; no global filters.") },
                Flags = PersistentFlag, ProviderKey = IntPtr.Zero, Weight = SubLayerWeight
            };
            Check(Native.FwpmSubLayerAdd0(engine, ref value, IntPtr.Zero), "Add dedicated persistent sublayer");
        }

        private static SubLayerInfo ReadSubLayer(IntPtr engine)
        {
            Guid key = SubLayerKey;
            IntPtr pointer = IntPtr.Zero;
            uint result = Native.FwpmSubLayerGetByKey0(engine, ref key, out pointer);
            try
            {
                if (result == SubLayerNotFound) return null;
                Check(result, "Read dedicated sublayer");
                NativeSubLayer value = Marshal.PtrToStructure<NativeSubLayer>(pointer);
                SubLayerInfo info = new SubLayerInfo {
                    Key = value.Key, Flags = value.Flags, Weight = value.Weight,
                    ProviderIsNull = value.ProviderKey == IntPtr.Zero,
                    Name = Marshal.PtrToStringUni(value.Display.Name)
                };
                info.MatchesExpected = info.Key == key && info.ProviderIsNull &&
                    info.Flags == PersistentFlag && info.Weight == SubLayerWeight && info.Name == SubLayerName;
                return info;
            }
            finally { if (pointer != IntPtr.Zero) Native.FwpmFreeMemory0(ref pointer); }
        }

        private static FilterInfo ReadFilter(IntPtr engine, Guid key)
        {
            IntPtr pointer = IntPtr.Zero;
            uint result = Native.FwpmFilterGetByKey0(engine, ref key, out pointer);
            try
            {
                if (result == FilterNotFound) return null;
                Check(result, "Read exact filter " + key);
                NativeFilter value = Marshal.PtrToStructure<NativeFilter>(pointer);
                FilterInfo info = new FilterInfo {
                    Key = value.Key, Layer = value.LayerKey, SubLayer = value.SubLayerKey,
                    Id = value.FilterId, Flags = value.Flags, Action = value.Action.Type,
                    Name = Marshal.PtrToStringUni(value.Display.Name),
                    ProviderIsNull = value.ProviderKey == IntPtr.Zero,
                    ConditionCount = value.NumFilterConditions, AppId = new byte[0]
                };
                if (value.NumFilterConditions == 2 && value.Conditions != IntPtr.Zero)
                {
                    int stride = Marshal.SizeOf(typeof(FilterCondition));
                    for (int i = 0; i < 2; i++)
                    {
                        FilterCondition c = Marshal.PtrToStructure<FilterCondition>(IntPtr.Add(value.Conditions, stride * i));
                        if (c.FieldKey == AppIdKey && c.MatchType == MatchEqual &&
                            c.ConditionValue.Type == TypeByteBlob && c.ConditionValue.Pointer != IntPtr.Zero)
                        {
                            info.AppId = ReadBlob(c.ConditionValue.Pointer);
                            info.NativeAppPath = Encoding.Unicode.GetString(info.AppId).TrimEnd('\0');
                            info.ExactAppIdCondition = info.AppId.Length > 2 && info.AppId.Length % 2 == 0 &&
                                info.NativeAppPath.StartsWith("\\device\\", StringComparison.OrdinalIgnoreCase) &&
                                info.NativeAppPath.IndexOfAny(new char[] { '*', '?', '\0' }) < 0;
                        }
                        if (c.FieldKey == FlagsKey && c.MatchType == MatchFlagsNoneSet &&
                            c.ConditionValue.Type == TypeUint32 && c.ConditionValue.Uint32 == LoopbackFlag)
                            info.NonLoopbackCondition = true;
                    }
                }
                info.IsOwnedExactBlock = info.Key == key && info.Name == FilterName &&
                    info.SubLayer == SubLayerKey && (info.Layer == LayerV4 || info.Layer == LayerV6) &&
                    info.ProviderIsNull && info.Action == ActionBlock && info.ConditionCount == 2 &&
                    info.ExactAppIdCondition && info.NonLoopbackCondition &&
                    (info.Flags & ~(PersistentFlag | DisabledFlag | IndexedFlag)) == 0;
                return info;
            }
            finally { if (pointer != IntPtr.Zero) Native.FwpmFreeMemory0(ref pointer); }
        }

        private static FilterInfo RequireMatch(FilterInfo info, byte[] appId, Guid layer, bool persistent)
        {
            if (info == null || !info.IsOwnedExactBlock || !info.Enabled || info.Layer != layer ||
                info.Persistent != persistent || !BytesEqual(info.AppId, appId))
                throw new InvalidOperationException("Filter is missing or differs from the exact requested application/layer/scope.");
            return info;
        }

        private static byte[] ReadBlob(IntPtr pointer)
        {
            if (pointer == IntPtr.Zero) throw new InvalidOperationException("Missing application ID blob.");
            ByteBlob blob = Marshal.PtrToStructure<ByteBlob>(pointer);
            if (blob.Size == 0 || blob.Size > 131072 || blob.Data == IntPtr.Zero)
                throw new InvalidOperationException("Invalid application ID blob size or data.");
            byte[] bytes = new byte[checked((int)blob.Size)];
            Marshal.Copy(blob.Data, bytes, 0, bytes.Length);
            return bytes;
        }

        private static bool BytesEqual(byte[] a, byte[] b)
        {
            if (a.Length != b.Length) return false;
            for (int i = 0; i < a.Length; i++) if (a[i] != b[i]) return false;
            return true;
        }

        private static string ValidatePath(string path, bool requireExists)
        {
            if (String.IsNullOrWhiteSpace(path) || path.IndexOfAny(new char[] { '*', '?', '\0' }) >= 0 ||
                path.Length < 3 || !Char.IsLetter(path[0]) || path[1] != ':' || (path[2] != '\\' && path[2] != '/'))
                throw new ArgumentException("An absolute local EXE path without wildcards is required.", "path");
            string full = Path.GetFullPath(path);
            if (!String.Equals(Path.GetExtension(full), ".exe", StringComparison.OrdinalIgnoreCase) ||
                (requireExists && !File.Exists(full)))
                throw new ArgumentException("The exact application EXE must exist.", "path");
            return full;
        }

        private static void ValidateKeys(Guid[] keys)
        {
            if (keys == null || keys.Length == 0 || keys.Length > 128)
                throw new ArgumentException("Supply between 1 and 128 explicit filter GUIDs.", "keys");
            HashSet<Guid> seen = new HashSet<Guid>();
            foreach (Guid key in keys)
                if (key == Guid.Empty || !seen.Add(key)) throw new ArgumentException("Filter GUIDs must be nonempty and distinct.", "keys");
        }

        private static void Check(uint result, string operation)
        {
            if (result != 0) throw new InvalidOperationException(operation + " failed: 0x" + result.ToString("X8") + " (" + result + ")");
        }

        private static void Size<T>(int expected)
        {
            int actual = Marshal.SizeOf(typeof(T));
            if (actual != expected) throw new PlatformNotSupportedException(typeof(T).Name + " ABI size " + actual + " != " + expected);
        }
        private static void Offset<T>(string field, int expected)
        {
            long actual = Marshal.OffsetOf(typeof(T), field).ToInt64();
            if (actual != expected) throw new PlatformNotSupportedException(typeof(T).Name + "." + field + " ABI offset " + actual + " != " + expected);
        }

        private sealed class Engine : IDisposable
        {
            internal IntPtr Handle;
            internal Engine()
            {
                ValidateAbi();
                if (Environment.OSVersion.Platform != PlatformID.Win32NT) throw new PlatformNotSupportedException("Windows required.");
                // 真正的 C# null，避免 PowerShell 参数绑定把 null 转成空字符串。
                Check(Native.FwpmEngineOpen0(null, 10, IntPtr.Zero, IntPtr.Zero, out Handle), "Open local nondynamic WFP engine");
            }
            public void Dispose()
            {
                if (Handle != IntPtr.Zero) { Native.FwpmEngineClose0(Handle); Handle = IntPtr.Zero; }
            }
        }

        private sealed class Arena : IDisposable
        {
            private readonly List<IntPtr> allocations = new List<IntPtr>();
            internal IntPtr Allocate(int size)
            {
                IntPtr pointer = Marshal.AllocHGlobal(size);
                allocations.Add(pointer);
                Marshal.Copy(new byte[size], 0, pointer, size);
                return pointer;
            }
            internal IntPtr String(string value)
            {
                IntPtr pointer = Marshal.StringToHGlobalUni(value);
                allocations.Add(pointer);
                return pointer;
            }
            public void Dispose()
            {
                for (int i = allocations.Count - 1; i >= 0; i--) Marshal.FreeHGlobal(allocations[i]);
                allocations.Clear();
            }
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct DisplayData { public IntPtr Name; public IntPtr Description; }
        [StructLayout(LayoutKind.Sequential)]
        private struct ByteBlob { public uint Size; public IntPtr Data; }
        [StructLayout(LayoutKind.Explicit, Size = 16)]
        private struct Value {
            [FieldOffset(0)] public int Type;
            [FieldOffset(8)] public uint Uint32;
            [FieldOffset(8)] public IntPtr Pointer;
        }
        [StructLayout(LayoutKind.Sequential)]
        private struct FilterCondition { public Guid FieldKey; public int MatchType; public Value ConditionValue; }
        [StructLayout(LayoutKind.Sequential)]
        private struct FilterAction { public uint Type; public Guid FilterType; }
        [StructLayout(LayoutKind.Explicit, Size = 16)]
        private struct FilterContext {
            [FieldOffset(0)] public ulong RawContext;
            [FieldOffset(0)] public Guid ProviderContextKey;
        }
        [StructLayout(LayoutKind.Sequential)]
        private struct NativeFilter {
            public Guid Key; public DisplayData Display; public uint Flags; public IntPtr ProviderKey;
            public ByteBlob ProviderData; public Guid LayerKey; public Guid SubLayerKey; public Value Weight;
            public uint NumFilterConditions; public IntPtr Conditions; public FilterAction Action;
            public FilterContext Context; public IntPtr Reserved; public ulong FilterId; public Value EffectiveWeight;
        }
        [StructLayout(LayoutKind.Sequential)]
        private struct NativeSubLayer {
            public Guid Key; public DisplayData Display; public uint Flags; public IntPtr ProviderKey;
            public ByteBlob ProviderData; public ushort Weight;
        }

        private static class Native
        {
            private const string Dll = "fwpuclnt.dll";
            [DllImport(Dll, ExactSpelling = true, CharSet = CharSet.Unicode)]
            internal static extern uint FwpmEngineOpen0(string serverName, uint authenticationService, IntPtr authenticationIdentity, IntPtr session, out IntPtr engine);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmEngineClose0(IntPtr engine);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmTransactionBegin0(IntPtr engine, uint flags);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmTransactionCommit0(IntPtr engine);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmTransactionAbort0(IntPtr engine);
            [DllImport(Dll, ExactSpelling = true, CharSet = CharSet.Unicode)]
            internal static extern uint FwpmGetAppIdFromFileName0(string fileName, out IntPtr appId);
            [DllImport(Dll, ExactSpelling = true)] internal static extern void FwpmFreeMemory0(ref IntPtr memory);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmSubLayerAdd0(IntPtr engine, ref NativeSubLayer subLayer, IntPtr securityDescriptor);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmSubLayerGetByKey0(IntPtr engine, ref Guid key, out IntPtr subLayer);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmFilterAdd0(IntPtr engine, ref NativeFilter filter, IntPtr securityDescriptor, out ulong id);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmFilterGetByKey0(IntPtr engine, ref Guid key, out IntPtr filter);
            [DllImport(Dll, ExactSpelling = true)] internal static extern uint FwpmFilterDeleteByKey0(IntPtr engine, ref Guid key);
        }
    }
}
