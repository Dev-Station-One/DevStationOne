package dev.devstationone.ui.kit

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.devstationone.net.ConnState
import dev.devstationone.ui.theme.Mux

/** Rounded surface with a thin outline; the selected one gets a mint outline. */
@Composable
fun MuxCard(
    modifier: Modifier = Modifier,
    selected: Boolean = false,
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val border = BorderStroke(if (selected) 1.5.dp else 1.dp, if (selected) Mux.colors.mint else MaterialTheme.colorScheme.outline)
    val colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)
    if (onClick != null) Card(onClick, modifier.fillMaxWidth(), shape = MaterialTheme.shapes.medium, colors = colors, border = border, content = content)
    else Card(modifier.fillMaxWidth(), shape = MaterialTheme.shapes.medium, colors = colors, border = border, content = content)
}

/** Chip color presets for terminal tabs (Tango palette, GNOME Terminal's menu colors). */
val TabChipColors: Map<String, Int> = linkedMapOf(
    "Blue" to 0xFF3465A4.toInt(), "Green" to 0xFF73D216.toInt(), "Orange" to 0xFFF57900.toInt(),
    "Purple" to 0xFF75507B.toInt(), "Red" to 0xFFCC0000.toInt(), "Yellow" to 0xFFEDD400.toInt(),
)

/** Round tab shape with a straight bottom edge, like a folder tab. */
val TabShape = RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp)

/** Pill; selected is solid mint. [dot] is a status color shown before the label.
 *  [color] paints a thick bottom edge (tab chips); [shape] overrides the pill. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MuxChip(
    label: String, selected: Boolean, onClick: () -> Unit,
    dot: Color? = null, onLongClick: (() -> Unit)? = null,
    color: Color? = null, shape: Shape = CircleShape,
) {
    val m = Mux.colors
    Row(
        Modifier.clip(shape)
            .background(if (selected) m.mint else MaterialTheme.colorScheme.surfaceVariant)
            .then(
                if (color != null) Modifier.drawBehind {
                    drawRect(color, topLeft = Offset(0f, size.height - 5.dp.toPx()), size = Size(size.width, 5.dp.toPx()))
                } else Modifier
            )
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (dot != null) {
            StatusDot(dot)
            Spacer(Modifier.width(6.dp))
        }
        Text(label, color = if (selected) m.onMint else MaterialTheme.colorScheme.onSurface, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** One dropdown entry; [detail] (an item count, say) is shown in parentheses after [text]. */
data class DropdownOption<T>(val value: T, val text: String, val detail: String? = null)

private fun DropdownOption<*>.shown() = if (detail == null) text else "$text ($detail)"

/**
 * Labelled dropdown selector (round 4, D4): a ≥ 48 dp button showing `label: choice ▾` over a Material3
 * `DropdownMenu`. A [selected] value missing from [options] is shown as its raw string, never dropped.
 * Disabled keeps the anchor visible but the menu closed.
 */
@Composable
fun <T> MuxDropdown(
    label: String, selected: T?, options: List<DropdownOption<T>>, onSelect: (T) -> Unit,
    modifier: Modifier = Modifier, enabled: Boolean = true,
) {
    var open by remember { mutableStateOf(false) }
    val shown = options.firstOrNull { it.value == selected }?.shown() ?: selected?.toString().orEmpty()
    Box(modifier) {
        TextButton(
            onClick = { open = true }, enabled = enabled,
            modifier = Modifier.heightIn(min = 48.dp).semantics {
                contentDescription = "$label, $shown"
                stateDescription = if (open) "expanded" else "collapsed"
                role = Role.DropdownList
            },
        ) { Text("$label: $shown ▾") }
        DropdownMenu(open && enabled, onDismissRequest = { open = false }) {
            options.forEach { o -> DropdownMenuItem(text = { Text(o.shown()) }, onClick = { open = false; onSelect(o.value) }) }
        }
    }
}

@Composable
fun MuxButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Button(
        onClick, modifier.fillMaxWidth().height(52.dp), enabled,
        shape = RoundedCornerShape(26.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = Mux.colors.mint, contentColor = Mux.colors.onMint,
            disabledContainerColor = MaterialTheme.colorScheme.surfaceVariant,
            disabledContentColor = MaterialTheme.colorScheme.onSurfaceVariant,
        ),
    ) { Text(text, style = MaterialTheme.typography.titleMedium) }
}

@Composable
fun MuxTonalButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Button(
        onClick, modifier.fillMaxWidth().height(52.dp),
        shape = RoundedCornerShape(26.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant, contentColor = MaterialTheme.colorScheme.onSurface,
        ),
    ) { Text(text, style = MaterialTheme.typography.titleMedium) }
}

@Composable
fun RoundIconButton(icon: ImageVector, description: String, onClick: () -> Unit) {
    Box(
        Modifier.size(40.dp).clip(CircleShape).background(MaterialTheme.colorScheme.surfaceVariant).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, description, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurface) }
}

/** Replaces TopAppBar: optional circular back button, [leading] slot, title, then [actions]. */
@Composable
fun ScreenHeader(
    title: String,
    onBack: (() -> Unit)? = null,
    leading: @Composable () -> Unit = {},
    actions: @Composable RowScope.() -> Unit = {},
) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        if (onBack != null) {
            RoundIconButton(MuxIcons.Back, "Back", onBack)
            Spacer(Modifier.width(12.dp))
        }
        leading()
        Text(
            title, style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f).padding(start = if (onBack == null) 8.dp else 0.dp),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically, content = actions)
    }
}

@Composable
fun StatusDot(color: Color) {
    Box(Modifier.size(8.dp).clip(CircleShape).background(color))
}

/** Dot color for a tab's connection state. */
@Composable
fun stateColor(s: ConnState?): Color = when (s) {
    ConnState.CONNECTED -> Mux.colors.mint
    ConnState.RECONNECTING -> Mux.colors.warn
    else -> MaterialTheme.colorScheme.onSurfaceVariant
}

// Hand-drawn 24x24 stroke icons (no icon library); tinted by Icon().
private fun icon(name: String, block: androidx.compose.ui.graphics.vector.ImageVector.Builder.() -> Unit) =
    ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).apply(block).build()

private fun ImageVector.Builder.stroke(width: Float = 2f, block: androidx.compose.ui.graphics.vector.PathBuilder.() -> Unit) =
    path(
        stroke = SolidColor(Color.Black), strokeLineWidth = width,
        strokeLineCap = StrokeCap.Round, strokeLineJoin = StrokeJoin.Round, pathBuilder = block,
    )

object MuxIcons {
    val Back = icon("back") { stroke { moveTo(15f, 5f); lineTo(8f, 12f); lineTo(15f, 19f) } }
    val Plus = icon("plus") { stroke { moveTo(12f, 5f); lineTo(12f, 19f); moveTo(5f, 12f); lineTo(19f, 12f) } }
    val Play = icon("play") { stroke { moveTo(7f, 5f); lineTo(19f, 12f); lineTo(7f, 19f); close() } }
    val More = icon("more") {
        stroke(3f) { moveTo(12f, 5f); lineTo(12f, 5.01f); moveTo(12f, 12f); lineTo(12f, 12.01f); moveTo(12f, 19f); lineTo(12f, 19.01f) }
    }
    val Folder = icon("folder") {
        stroke { moveTo(3f, 6f); lineTo(10f, 6f); lineTo(12f, 8f); lineTo(21f, 8f); lineTo(21f, 19f); lineTo(3f, 19f); close() }
    }
    val File = icon("file") {
        stroke { moveTo(6f, 3f); lineTo(14f, 3f); lineTo(19f, 8f); lineTo(19f, 21f); lineTo(6f, 21f); close() }
        stroke { moveTo(14f, 3f); lineTo(14f, 8f); lineTo(19f, 8f) }
    }
    val Server = icon("server") {
        stroke { moveTo(4f, 4f); lineTo(20f, 4f); lineTo(20f, 10f); lineTo(4f, 10f); close() }
        stroke { moveTo(4f, 14f); lineTo(20f, 14f); lineTo(20f, 20f); lineTo(4f, 20f); close() }
        stroke(3f) { moveTo(8f, 7f); lineTo(8f, 7.01f); moveTo(8f, 17f); lineTo(8f, 17.01f) }
    }
    // Workspace-tab round-2 set (docs/DESIGN_ANDROID_SCM.md §2): Prompt =
    // Terminal, Note = Journal, Branch = Source Control; Folder serves Files.
    val Prompt = icon("prompt") {
        stroke { moveTo(4f, 6f); lineTo(10f, 12f); lineTo(4f, 18f) }
        stroke(3f) { moveTo(13f, 18f); lineTo(20f, 18f) }
    }
    val Note = icon("note") {
        stroke { moveTo(5f, 3f); lineTo(19f, 3f); lineTo(19f, 21f); lineTo(5f, 21f); close() }
        stroke { moveTo(8f, 8f); lineTo(16f, 8f); moveTo(8f, 12f); lineTo(16f, 12f); moveTo(8f, 16f); lineTo(13f, 16f) }
    }
    val Branch = icon("branch") {
        circle(7f, 6f, 2.2f)
        circle(17f, 6f, 2.2f)
        circle(7f, 18f, 2.2f)
        stroke { moveTo(7f, 8.2f); lineTo(7f, 15.8f) }
        stroke {
            moveTo(17f, 8.2f)
            quadTo(17f, 13f, 10.5f, 13.6f)
            lineTo(9.2f, 13.8f)
        }
    }
    // Databases tab: a cylinder (top ellipse, two bands).
    val Database = icon("database") {
        stroke {
            moveTo(4f, 6f)
            arcTo(8f, 3f, 0f, true, true, 20f, 6f)
            arcTo(8f, 3f, 0f, true, true, 4f, 6f)
        }
        stroke { moveTo(4f, 6f); lineTo(4f, 18f); arcTo(8f, 3f, 0f, false, false, 20f, 18f); lineTo(20f, 6f) }
        stroke { moveTo(4f, 12f); arcTo(8f, 3f, 0f, false, false, 20f, 12f) }
    }

}

/** One stroke-outline circle at (cx, cy), drawn as two half arcs. */
private fun ImageVector.Builder.circle(cx: Float, cy: Float, r: Float) {
    stroke {
        moveTo(cx - r, cy)
        arcTo(r, r, 0f, true, true, cx + r, cy)
        arcTo(r, r, 0f, true, true, cx - r, cy)
    }
}
